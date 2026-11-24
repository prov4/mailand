#include "MailWeb.h"

#include <QBuffer>
#include <QCoreApplication>
#include <QDesktopServices>
#include <QJSEngine>
#include <QPointer>
#include <QQmlEngine>
#include <QRegularExpression>
#include <QtWebEngineCore/QWebEngineUrlRequestInfo>
#include <QtWebEngineCore/QWebEngineUrlRequestInterceptor>
#include <QtWebEngineCore/QWebEngineUrlRequestJob>
#include <QtWebEngineCore/QWebEngineUrlScheme>
#include <QtWebEngineCore/QWebEngineUrlSchemeHandler>
#include <QtWebEngineQuick/QQuickWebEngineProfile>

#include "Rpc.h"

namespace {

const QRegularExpression kMessageId(QStringLiteral("^[0-9a-fA-F]{8,64}$"));

QString normCid(QString cid)
{
    cid = cid.trimmed();
    if (cid.startsWith(QLatin1Char('<')) && cid.endsWith(QLatin1Char('>')))
        cid = cid.mid(1, cid.size() - 2);
    return cid;
}

void replyBuffer(QWebEngineUrlRequestJob *job, const QByteArray &mime, const QByteArray &data)
{
    auto *buf = new QBuffer(job); // dies with the job
    buf->setData(data);
    buf->open(QIODevice::ReadOnly);
    job->reply(mime, buf);
}

class MessageHandler : public QWebEngineUrlSchemeHandler
{
public:
    explicit MessageHandler(MailWeb *web) : QWebEngineUrlSchemeHandler(web), m_web(web) {}

    void requestStarted(QWebEngineUrlRequestJob *job) override
    {
        const QString id = job->requestUrl().host();
        if (!kMessageId.match(id).hasMatch()) {
            job->fail(QWebEngineUrlRequestJob::UrlInvalid);
            return;
        }
        // Only the document itself lives at "/"; relative refs in the mail
        // (e.g. <img src="x">) must not re-serve the whole message.
        if (const QString path = job->requestUrl().path(); !path.isEmpty() && path != QLatin1String("/")) {
            job->fail(QWebEngineUrlRequestJob::UrlNotFound);
            return;
        }
        QPointer<QWebEngineUrlRequestJob> guard(job);
        QPointer<MailWeb> web(m_web);
        Rpc::instance()->getMessage(
            id, job, [guard, web, id](const grpc::Status &st, const mailand::v1::MessageDetail &d) {
                if (!guard || !web)
                    return;
                if (!st.ok()) {
                    guard->fail(st.error_code() == grpc::StatusCode::NOT_FOUND
                                    ? QWebEngineUrlRequestJob::UrlNotFound
                                    : QWebEngineUrlRequestJob::RequestFailed);
                    return;
                }
                QHash<QString, MailWeb::CidPart> cids;
                for (const auto &a : d.attachments()) {
                    const QString cid = normCid(QString::fromStdString(a.content_id()));
                    if (!cid.isEmpty())
                        cids.insert(cid, {QString::fromStdString(a.id()), QString::fromStdString(a.mime_type())});
                }
                web->rememberMessage(id, cids);

                const QString csp = web->contentSecurityPolicy();
                QString html = QString::fromStdString(d.html_body());
                // The CSP rejects remote resources inside Blink, before they ever
                // reach the interceptor, so detect them here to drive the
                // "Load remote images" banner.
                static const QRegularExpression kRemoteRef(
                    QStringLiteral(R"((?:\b(?:src|srcset|background|poster)\s*=\s*["']?\s*|url\(\s*["']?\s*|<link\b[^>]*\bhref\s*=\s*["']?\s*)(?:https?:)?//)"),
                    QRegularExpression::CaseInsensitiveOption);
                if (!web->remoteAllowed() && html.contains(kRemoteRef))
                    web->blockedRemote();
                if (html.isEmpty())
                    html = QStringLiteral("<pre style=\"white-space:pre-wrap\">%1</pre>")
                               .arg(QString::fromStdString(d.text_body()).toHtmlEscaped());
                QMultiMap<QByteArray, QByteArray> headers;
                headers.insert("Content-Security-Policy", csp.toUtf8());
                headers.insert("X-Content-Type-Options", "nosniff");
                headers.insert("Referrer-Policy", "no-referrer");
                guard->setAdditionalResponseHeaders(headers);
                replyBuffer(guard, "text/html;charset=utf-8", html.toUtf8());
            });
    }

private:
    MailWeb *m_web;
};

class CidHandler : public QWebEngineUrlSchemeHandler
{
public:
    explicit CidHandler(MailWeb *web) : QWebEngineUrlSchemeHandler(web), m_web(web) {}

    void requestStarted(QWebEngineUrlRequestJob *job) override
    {
        const QString cid = normCid(job->requestUrl().path());
        QString messageId = m_web->lastMessageId();
        const QUrl init = job->initiator();
        if (init.scheme() == QLatin1String("mailand-msg") && !init.host().isEmpty())
            messageId = init.host();

        MailWeb::CidPart part;
        if (cid.isEmpty() || !m_web->cidLookup(messageId, cid, &part)) {
            job->fail(QWebEngineUrlRequestJob::UrlNotFound);
            return;
        }
        QPointer<QWebEngineUrlRequestJob> guard(job);
        Rpc::instance()->getAttachment(
            messageId, part.attachmentId, job,
            [guard, part](const grpc::Status &st, const mailand::v1::AttachmentData &a) {
                if (!guard)
                    return;
                if (!st.ok()) {
                    guard->fail(QWebEngineUrlRequestJob::UrlNotFound);
                    return;
                }
                QByteArray mime = QByteArray::fromStdString(a.info().mime_type());
                if (mime.isEmpty())
                    mime = part.mimeType.toUtf8();
                if (mime.isEmpty())
                    mime = "application/octet-stream";
                replyBuffer(guard, mime, QByteArray(a.data().data(), qsizetype(a.data().size())));
            });
    }

private:
    MailWeb *m_web;
};

class Interceptor : public QWebEngineUrlRequestInterceptor
{
public:
    explicit Interceptor(MailWeb *web) : QWebEngineUrlRequestInterceptor(web), m_web(web) {}

    // May be called off the GUI thread: only touches atomics and queued signals.
    void interceptRequest(QWebEngineUrlRequestInfo &info) override
    {
        const QUrl url = info.requestUrl();
        const QString scheme = url.scheme();
        const bool mainFrame = info.resourceType() == QWebEngineUrlRequestInfo::ResourceTypeMainFrame;

        if (scheme == QLatin1String("mailand-msg")) {
            if (mainFrame)
                m_web->pageLoadStarted();
            return;
        }
        if (scheme == QLatin1String("cid") || scheme == QLatin1String("data")
            || scheme == QLatin1String("blob") || scheme == QLatin1String("about"))
            return;
        if (scheme == QLatin1String("http") || scheme == QLatin1String("https")) {
            if (mainFrame || info.resourceType() == QWebEngineUrlRequestInfo::ResourceTypeSubFrame) {
                info.block(true); // links never navigate inside the viewer
                return;
            }
            if (!m_web->remoteAllowed()) {
                info.block(true);
                m_web->blockedRemote();
            }
            return;
        }
        info.block(true); // file:, ftp:, anything else
    }

private:
    MailWeb *m_web;
};

} // namespace

void MailWeb::registerSchemes()
{
    QWebEngineUrlScheme msg(QByteArrayLiteral("mailand-msg"));
    msg.setSyntax(QWebEngineUrlScheme::Syntax::Host);
    msg.setDefaultPort(QWebEngineUrlScheme::PortUnspecified);
    msg.setFlags(QWebEngineUrlScheme::SecureScheme | QWebEngineUrlScheme::CorsEnabled);
    QWebEngineUrlScheme::registerScheme(msg);

    QWebEngineUrlScheme cid(QByteArrayLiteral("cid"));
    cid.setSyntax(QWebEngineUrlScheme::Syntax::Path);
    cid.setFlags(QWebEngineUrlScheme::SecureScheme | QWebEngineUrlScheme::CorsEnabled);
    QWebEngineUrlScheme::registerScheme(cid);
}

MailWeb *MailWeb::instance()
{
    static MailWeb *inst = new MailWeb(QCoreApplication::instance());
    return inst;
}

MailWeb *MailWeb::create(QQmlEngine *, QJSEngine *)
{
    MailWeb *w = instance();
    QJSEngine::setObjectOwnership(w, QJSEngine::CppOwnership);
    return w;
}

MailWeb::MailWeb(QObject *parent) : QObject(parent)
{
    m_profile = new QQuickWebEngineProfile(this);
    m_profile->setOffTheRecord(true);
    m_profile->setHttpCacheType(QQuickWebEngineProfile::MemoryHttpCache);
    m_profile->setPersistentCookiesPolicy(QQuickWebEngineProfile::NoPersistentCookies);

    m_interceptor = new Interceptor(this);
    m_profile->setUrlRequestInterceptor(m_interceptor);
    m_msgHandler = new MessageHandler(this);
    m_cidHandler = new CidHandler(this);
    m_profile->installUrlSchemeHandler("mailand-msg", m_msgHandler);
    m_profile->installUrlSchemeHandler("cid", m_cidHandler);
}

MailWeb::~MailWeb() = default;

void MailWeb::setRemoteAllowed(bool allowed)
{
    if (m_remoteAllowed.exchange(allowed) == allowed)
        return;
    emit remoteAllowedChanged();
}

void MailWeb::openExternal(const QUrl &url)
{
    const QString s = url.scheme().toLower();
    if (s == QLatin1String("http") || s == QLatin1String("https") || s == QLatin1String("mailto"))
        QDesktopServices::openUrl(url);
}

QString MailWeb::contentSecurityPolicy() const
{
    QString img = QStringLiteral("img-src cid: data: mailand-msg:");
    if (m_remoteAllowed.load())
        img += QStringLiteral(" http: https:");
    return QStringLiteral("default-src 'none'; ") + img
           + QStringLiteral("; style-src 'unsafe-inline' mailand-msg:; font-src data:;");
}

void MailWeb::rememberMessage(const QString &messageId, const QHash<QString, CidPart> &cids)
{
    if (m_cids.size() > 16)
        m_cids.clear();
    m_cids.insert(messageId, cids);
    m_lastMessage = messageId;
}

bool MailWeb::cidLookup(const QString &messageId, const QString &cid, CidPart *out) const
{
    const auto m = m_cids.constFind(messageId);
    if (m == m_cids.constEnd())
        return false;
    const auto p = m->constFind(cid);
    if (p == m->constEnd())
        return false;
    *out = *p;
    return true;
}

void MailWeb::blockedRemote()
{
    if (m_notified.exchange(true))
        return;
    QMetaObject::invokeMethod(this, [this] { emit remoteContentBlocked(); }, Qt::QueuedConnection);
}
