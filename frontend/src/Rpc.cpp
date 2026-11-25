#include "Rpc.h"

#include <QCoreApplication>
#include <QDir>
#include <QJSEngine>
#include <QPointer>
#include <QQmlEngine>
#include <QVariantMap>

#include <unistd.h>

#include <chrono>

using namespace std::chrono_literals;
using mailand::v1::MailandService;

namespace {

std::shared_ptr<grpc::Channel> makeChannel()
{
    grpc::ChannelArguments args;
    args.SetMaxReceiveMessageSize(64 * 1024 * 1024);
    args.SetMaxSendMessageSize(64 * 1024 * 1024);
    return grpc::CreateCustomChannel("unix:" + Rpc::socketPath().toStdString(),
                                     grpc::InsecureChannelCredentials(), args);
}

QString s(const std::string &v) { return QString::fromStdString(v); }

} // namespace

Rpc *Rpc::instance()
{
    static Rpc *inst = new Rpc(QCoreApplication::instance());
    return inst;
}

Rpc *Rpc::create(QQmlEngine *, QJSEngine *)
{
    Rpc *r = instance();
    QJSEngine::setObjectOwnership(r, QJSEngine::CppOwnership);
    return r;
}

Rpc::Rpc(QObject *parent) : QObject(parent), m_channel(makeChannel())
{
    m_thread = std::thread([this] { eventLoop(); });
}

Rpc::~Rpc()
{
    shutdown();
}

void Rpc::shutdown()
{
    {
        std::lock_guard<std::mutex> lock(m_mutex);
        m_stopping = true;
        if (m_streamCtx)
            m_streamCtx->TryCancel();
    }
    m_cv.notify_all();
    if (m_thread.joinable())
        m_thread.join();
}

QString Rpc::socketPath()
{
    if (const QByteArray p = qgetenv("MAILAND_SOCKET"); !p.isEmpty())
        return QString::fromLocal8Bit(p);
    if (const QByteArray d = qgetenv("XDG_RUNTIME_DIR"); !d.isEmpty())
        return QDir(QString::fromLocal8Bit(d)).filePath(QStringLiteral("mailand.sock"));
    return QStringLiteral("/tmp/mailand-%1.sock").arg(getuid());
}

QString Rpc::statusText(const grpc::Status &st)
{
    return s(st.error_message().empty() ? "gRPC error" : st.error_message())
           + QStringLiteral(" (code %1)").arg(static_cast<int>(st.error_code()));
}

void Rpc::reportError(const QString &message)
{
    emit errorOccurred(message);
}

template <class Resp, class Req, class Invoke>
void Rpc::call(Req req, Invoke invoke, QObject *ctx, Done<Resp> done, int deadlineMs,
               bool waitForReady)
{
    // Everything the in-flight call touches lives here so it outlives callers and models.
    struct State {
        std::unique_ptr<MailandService::Stub> stub;
        grpc::ClientContext cx;
        Req req;
        Resp resp;
    };
    auto st = std::make_shared<State>();
    st->stub = MailandService::NewStub(m_channel);
    st->req = std::move(req);
    st->cx.set_deadline(std::chrono::system_clock::now() + std::chrono::milliseconds(deadlineMs));
    if (waitForReady)
        st->cx.set_wait_for_ready(true);

    QPointer<QObject> guard(ctx ? ctx : this);
    QPointer<Rpc> self(this);
    std::function<void(grpc::Status)> cb = [st, guard, self, done = std::move(done)](grpc::Status status) {
        // gRPC thread: hop to the GUI thread via the (always alive) application object.
        QCoreApplication *app = QCoreApplication::instance();
        if (!app)
            return;
        QMetaObject::invokeMethod(
            app,
            [st, guard, self, done, status] {
                if (self) {
                    if (status.ok())
                        self->setConnected(true, QString());
                    else if (status.error_code() == grpc::StatusCode::UNAVAILABLE)
                        self->setConnected(false, statusText(status));
                }
                if (guard && done)
                    done(status, st->resp);
            },
            Qt::QueuedConnection);
    };
    invoke(st->stub->async(), &st->cx, &st->req, &st->resp, std::move(cb));
}

void Rpc::refreshAccounts()
{
    call<mailand::v1::AccountList>(
        mailand::v1::Empty(),
        [](auto *a, auto *cx, const auto *rq, auto *rs, auto cb) { a->ListAccounts(cx, rq, rs, std::move(cb)); },
        this,
        [this](const grpc::Status &status, const mailand::v1::AccountList &resp) {
            if (!status.ok()) {
                setConnected(false, statusText(status));
                return;
            }
            QVariantList list;
            for (const auto &a : resp.accounts())
                list.append(QVariantMap{{QStringLiteral("id"), s(a.id())},
                                        {QStringLiteral("name"), s(a.name())},
                                        {QStringLiteral("email"), s(a.email())},
                                        {QStringLiteral("signature"), s(a.signature())}});
            m_accounts = list;
            emit accountsChanged();
        },
        3000, true);
}

void Rpc::triggerSync(const QString &accountId)
{
    QStringList ids;
    if (accountId.isEmpty()) {
        for (const auto &v : std::as_const(m_accounts))
            ids << v.toMap().value(QStringLiteral("id")).toString();
    } else {
        ids << accountId;
    }
    for (const QString &id : std::as_const(ids)) {
        mailand::v1::AccountRequest req;
        req.set_account_id(id.toStdString());
        call<mailand::v1::Empty>(
            std::move(req),
            [](auto *a, auto *cx, const auto *rq, auto *rs, auto cb) { a->TriggerSync(cx, rq, rs, std::move(cb)); },
            this,
            [this](const grpc::Status &status, const mailand::v1::Empty &) {
                if (!status.ok())
                    reportError(QStringLiteral("Sync failed: ") + statusText(status));
            });
    }
}

void Rpc::listFolders(const QString &accountId, QObject *ctx, Done<mailand::v1::FolderList> done)
{
    mailand::v1::AccountRequest req;
    req.set_account_id(accountId.toStdString());
    call<mailand::v1::FolderList>(
        std::move(req),
        [](auto *a, auto *cx, const auto *rq, auto *rs, auto cb) { a->ListFolders(cx, rq, rs, std::move(cb)); },
        ctx, std::move(done));
}

void Rpc::listMessages(const QString &folderId, quint32 limit, quint32 offset, bool unreadOnly,
                       QObject *ctx, Done<mailand::v1::MessageHeaderList> done)
{
    mailand::v1::ListMessagesRequest req;
    req.set_folder_id(folderId.toStdString());
    req.set_limit(limit);
    req.set_offset(offset);
    req.set_unread_only(unreadOnly);
    call<mailand::v1::MessageHeaderList>(
        std::move(req),
        [](auto *a, auto *cx, const auto *rq, auto *rs, auto cb) { a->ListMessages(cx, rq, rs, std::move(cb)); },
        ctx, std::move(done));
}

void Rpc::searchMessages(const QString &query, const QString &accountId, quint32 limit,
                         bool unreadOnly, QObject *ctx, Done<mailand::v1::MessageHeaderList> done)
{
    mailand::v1::SearchRequest req;
    req.set_query(query.toStdString());
    req.set_account_id(accountId.toStdString());
    req.set_limit(limit);
    req.set_unread_only(unreadOnly);
    call<mailand::v1::MessageHeaderList>(
        std::move(req),
        [](auto *a, auto *cx, const auto *rq, auto *rs, auto cb) { a->SearchMessages(cx, rq, rs, std::move(cb)); },
        ctx, std::move(done));
}

void Rpc::getMessage(const QString &messageId, QObject *ctx, Done<mailand::v1::MessageDetail> done)
{
    mailand::v1::MessageRequest req;
    req.set_message_id(messageId.toStdString());
    call<mailand::v1::MessageDetail>(
        std::move(req),
        [](auto *a, auto *cx, const auto *rq, auto *rs, auto cb) { a->GetMessage(cx, rq, rs, std::move(cb)); },
        ctx, std::move(done), 20000);
}

void Rpc::getAttachment(const QString &messageId, const QString &attachmentId, QObject *ctx,
                        Done<mailand::v1::AttachmentData> done)
{
    mailand::v1::AttachmentRequest req;
    req.set_message_id(messageId.toStdString());
    req.set_attachment_id(attachmentId.toStdString());
    call<mailand::v1::AttachmentData>(
        std::move(req),
        [](auto *a, auto *cx, const auto *rq, auto *rs, auto cb) { a->GetAttachment(cx, rq, rs, std::move(cb)); },
        ctx, std::move(done), 60000);
}

void Rpc::setFlags(const QString &messageId, std::optional<bool> isRead,
                   std::optional<bool> isFlagged, QObject *ctx, Done<mailand::v1::Empty> done)
{
    mailand::v1::SetFlagsRequest req;
    req.add_message_ids(messageId.toStdString());
    if (isRead)
        req.set_is_read(*isRead);
    if (isFlagged)
        req.set_is_flagged(*isFlagged);
    call<mailand::v1::Empty>(
        std::move(req),
        [](auto *a, auto *cx, const auto *rq, auto *rs, auto cb) { a->SetMessageFlags(cx, rq, rs, std::move(cb)); },
        ctx, std::move(done));
}

void Rpc::archiveMessage(const QString &messageId, QObject *ctx, Done<mailand::v1::Empty> done)
{
    mailand::v1::ArchiveRequest req;
    req.add_message_ids(messageId.toStdString());
    call<mailand::v1::Empty>(
        std::move(req),
        [](auto *a, auto *cx, const auto *rq, auto *rs, auto cb) { a->ArchiveMessages(cx, rq, rs, std::move(cb)); },
        ctx, std::move(done), 20000);
}

void Rpc::sendMessage(mailand::v1::SendMessageRequest req, QObject *ctx,
                      Done<mailand::v1::SendMessageResponse> done)
{
    call<mailand::v1::SendMessageResponse>(
        std::move(req),
        [](auto *a, auto *cx, const auto *rq, auto *rs, auto cb) { a->SendMessage(cx, rq, rs, std::move(cb)); },
        ctx, std::move(done), 90000);
}

void Rpc::listCalendars(QObject *ctx, Done<mailand::v1::CalendarList> done)
{
    call<mailand::v1::CalendarList>(
        mailand::v1::Empty(),
        [](auto *a, auto *cx, const auto *rq, auto *rs, auto cb) { a->ListCalendars(cx, rq, rs, std::move(cb)); },
        ctx, std::move(done));
}

void Rpc::listEvents(qint64 startUnix, qint64 endUnix, QObject *ctx, Done<mailand::v1::EventList> done)
{
    mailand::v1::ListEventsRequest req;
    req.set_start_unix(startUnix);
    req.set_end_unix(endUnix);
    call<mailand::v1::EventList>(
        std::move(req),
        [](auto *a, auto *cx, const auto *rq, auto *rs, auto cb) { a->ListEvents(cx, rq, rs, std::move(cb)); },
        ctx, std::move(done), 20000);
}

void Rpc::respondToEvent(const QString &eventId, const QString &status, QObject *ctx,
                         Done<mailand::v1::Empty> done)
{
    mailand::v1::RespondToEventRequest req;
    req.set_event_id(eventId.toStdString());
    req.set_status(status.toStdString());
    call<mailand::v1::Empty>(
        std::move(req),
        [](auto *a, auto *cx, const auto *rq, auto *rs, auto cb) { a->RespondToEvent(cx, rq, rs, std::move(cb)); },
        ctx, std::move(done), 30000);
}

void Rpc::saveContact(mailand::v1::SaveContactRequest req, QObject *ctx, Done<mailand::v1::Contact> done)
{
    call<mailand::v1::Contact>(
        std::move(req),
        [](auto *a, auto *cx, const auto *rq, auto *rs, auto cb) { a->SaveContact(cx, rq, rs, std::move(cb)); },
        ctx, std::move(done), 30000);
}

void Rpc::deleteContact(const QString &contactId, QObject *ctx, Done<mailand::v1::Empty> done)
{
    mailand::v1::ContactRequest req;
    req.set_contact_id(contactId.toStdString());
    call<mailand::v1::Empty>(
        std::move(req),
        [](auto *a, auto *cx, const auto *rq, auto *rs, auto cb) { a->DeleteContact(cx, rq, rs, std::move(cb)); },
        ctx, std::move(done), 30000);
}

void Rpc::listMessagesByAddress(const QStringList &addresses, quint32 limit, QObject *ctx,
                                Done<mailand::v1::MessageHeaderList> done)
{
    mailand::v1::AddressMessagesRequest req;
    for (const QString &a : addresses)
        req.add_addresses(a.toStdString());
    req.set_limit(limit);
    call<mailand::v1::MessageHeaderList>(
        std::move(req),
        [](auto *a, auto *cx, const auto *rq, auto *rs, auto cb) { a->ListMessagesByAddress(cx, rq, rs, std::move(cb)); },
        ctx, std::move(done));
}

void Rpc::listContacts(const QString &query, quint32 limit, bool emailOnly, QObject *ctx,
                       Done<mailand::v1::ContactList> done)
{
    mailand::v1::ListContactsRequest req;
    req.set_query(query.toStdString());
    req.set_limit(limit);
    req.set_email_only(emailOnly);
    call<mailand::v1::ContactList>(
        std::move(req),
        [](auto *a, auto *cx, const auto *rq, auto *rs, auto cb) { a->ListContacts(cx, rq, rs, std::move(cb)); },
        ctx, std::move(done));
}

void Rpc::setConnected(bool connected, const QString &error)
{
    if (m_connected != connected) {
        m_connected = connected;
        // Sync state is only valid for the current event stream; a pass that was
        // running when the stream dropped never sends SYNC_FINISHED to us.
        if (!m_syncing.isEmpty() || !m_remaining.isEmpty()) {
            m_syncing.clear();
            m_remaining.clear();
            emit syncingChanged();
        }
        emit connectedChanged();
    }
    if (m_lastError != error) {
        m_lastError = error;
        emit lastErrorChanged();
    }
}

void Rpc::handleEvent(int type, const QString &accountId, const QString &folderId,
                      const QString &detail)
{
    const bool wasSyncing = syncing();
    const int wasRemaining = syncRemaining();
    if (type == mailand::v1::DaemonEvent::SYNC_STARTED) {
        m_syncing.insert(accountId);
    } else if (type == mailand::v1::DaemonEvent::SYNC_PROGRESS) {
        // Also marks syncing: the UI may have connected in the middle of a pass.
        m_syncing.insert(accountId);
        m_remaining.insert(accountId + QLatin1Char('\n') + folderId, detail.toInt());
    } else if (type == mailand::v1::DaemonEvent::SYNC_FINISHED) {
        // "backlog": the daemon starts the next chunk right away; keep the indicator steady.
        if (detail != QLatin1String("backlog")) {
            m_syncing.remove(accountId);
            const QString prefix = accountId + QLatin1Char('\n');
            m_remaining.removeIf([&](const auto &it) { return it.key().startsWith(prefix); });
        }
        if (m_reauthAccount == accountId) {
            m_reauthAccount.clear();
            emit reauthAccountChanged();
        }
    } else if (type == mailand::v1::DaemonEvent::ERROR && detail == QLatin1String("reauth_required")) {
        if (m_reauthAccount != accountId) {
            m_reauthAccount = accountId;
            emit reauthAccountChanged();
        }
    }
    if (wasSyncing != syncing() || wasRemaining != syncRemaining())
        emit syncingChanged();
    emit daemonEvent(type, accountId, folderId, detail);
}

int Rpc::syncRemaining() const
{
    int n = 0;
    for (int v : m_remaining)
        n += v;
    return n;
}

// Dedicated thread: blocking SubscribeEvents reader with reconnect/backoff (0.5s..10s).
void Rpc::eventLoop()
{
    int backoffMs = 500;
    auto stopping = [this] {
        std::lock_guard<std::mutex> lock(m_mutex);
        return m_stopping;
    };

    while (!stopping()) {
        m_channel->GetState(true); // kick off connecting
        bool up = false;
        while (!stopping()) {
            if (m_channel->WaitForConnected(std::chrono::system_clock::now() + 200ms)) {
                up = true;
                break;
            }
        }
        if (!up)
            break;

        const auto stub = MailandService::NewStub(m_channel);
        grpc::ClientContext cx;
        {
            std::lock_guard<std::mutex> lock(m_mutex);
            if (m_stopping)
                break;
            m_streamCtx = &cx;
        }
        QMetaObject::invokeMethod(this, [this] {
            setConnected(true, QString());
            refreshAccounts();
        }, Qt::QueuedConnection);

        const auto started = std::chrono::steady_clock::now();
        {
            mailand::v1::Empty req;
            auto reader = stub->SubscribeEvents(&cx, req);
            mailand::v1::DaemonEvent ev;
            while (reader->Read(&ev)) {
                const int type = static_cast<int>(ev.type());
                const QString acc = s(ev.account_id()), folder = s(ev.folder_id()), detail = s(ev.detail());
                QMetaObject::invokeMethod(this, [this, type, acc, folder, detail] {
                    handleEvent(type, acc, folder, detail);
                }, Qt::QueuedConnection);
            }
            reader->Finish();
        }
        {
            std::lock_guard<std::mutex> lock(m_mutex);
            m_streamCtx = nullptr;
            if (m_stopping)
                break;
        }
        QMetaObject::invokeMethod(this, [this] {
            setConnected(false, QStringLiteral("daemon connection lost"));
        }, Qt::QueuedConnection);

        backoffMs = (std::chrono::steady_clock::now() - started > 2s) ? 500 : std::min(backoffMs * 2, 10000);
        std::unique_lock<std::mutex> lock(m_mutex);
        m_cv.wait_for(lock, std::chrono::milliseconds(backoffMs), [this] { return m_stopping; });
    }
}

bool Rpc::listAccountsSync(QList<AccountInfo> *out, QString *error, int timeoutMs)
{
    auto stub = MailandService::NewStub(makeChannel());
    grpc::ClientContext ctx;
    ctx.set_deadline(std::chrono::system_clock::now() + std::chrono::milliseconds(timeoutMs));
    mailand::v1::Empty req;
    mailand::v1::AccountList resp;
    const grpc::Status status = stub->ListAccounts(&ctx, req, &resp);
    if (!status.ok()) {
        if (error)
            *error = QStringLiteral("ListAccounts via %1 failed: %2").arg(socketPath(), statusText(status));
        return false;
    }
    for (const auto &a : resp.accounts())
        out->append({s(a.id()), s(a.name()), s(a.email())});
    return true;
}
