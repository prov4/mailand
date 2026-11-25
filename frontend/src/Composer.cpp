#include "Composer.h"

#include <QFile>
#include <QFileInfo>
#include <QMimeDatabase>
#include <QRegularExpression>
#include <QSet>
#include <QVariantMap>

#include "Format.h"
#include "Rpc.h"

namespace {

constexpr qint64 kMaxAttachmentTotal = 25LL * 1024 * 1024;

QString s(const std::string &v) { return QString::fromStdString(v); }

QString senderText(const QString &name, const QString &addr)
{
    return name.isEmpty() ? addr : QStringLiteral("%1 <%2>").arg(name, addr);
}

} // namespace

Composer::Composer(QObject *parent) : QObject(parent) {}

// ---- helpers ------------------------------------------------------------------------------------

// Splits on ',' / ';' outside of quotes and angle brackets.
QStringList Composer::splitAddresses(const QString &list)
{
    QStringList out;
    QString cur;
    bool quoted = false, angle = false;
    auto flush = [&] {
        const QString t = cur.trimmed();
        if (!t.isEmpty())
            out << t;
        cur.clear();
    };
    for (const QChar c : list) {
        if (c == QLatin1Char('"'))
            quoted = !quoted;
        else if (!quoted && c == QLatin1Char('<'))
            angle = true;
        else if (!quoted && c == QLatin1Char('>'))
            angle = false;
        if (!quoted && !angle && (c == QLatin1Char(',') || c == QLatin1Char(';')))
            flush();
        else
            cur += c;
    }
    flush();
    return out;
}

QString Composer::bareAddress(const QString &addr)
{
    static const QRegularExpression re(QStringLiteral("<([^>]*)>"));
    const auto m = re.match(addr);
    return (m.hasMatch() ? m.captured(1) : addr).trimmed().toLower();
}

QString Composer::replySubject(const QString &subj)
{
    static const QRegularExpression re(QStringLiteral("^\\s*(re|odg)\\s*:"),
                                       QRegularExpression::CaseInsensitiveOption);
    return re.match(subj).hasMatch() ? subj : QStringLiteral("Re: ") + subj;
}

QString Composer::forwardSubject(const QString &subj)
{
    static const QRegularExpression re(QStringLiteral("^\\s*(fwd?|fw)\\s*:"),
                                       QRegularExpression::CaseInsensitiveOption);
    return re.match(subj).hasMatch() ? subj : QStringLiteral("Fwd: ") + subj;
}

QString Composer::quoteText(const QString &text)
{
    QString t = text;
    t.replace(QStringLiteral("\r\n"), QStringLiteral("\n"));
    while (t.endsWith(QLatin1Char('\n')))
        t.chop(1);
    QStringList lines = t.split(QLatin1Char('\n'));
    for (QString &l : lines)
        l = l.isEmpty() ? QStringLiteral(">") : QStringLiteral("> ") + l;
    return lines.join(QLatin1Char('\n'));
}

QString Composer::ownEmail() const
{
    const auto accounts = Rpc::instance()->accounts();
    for (const QVariant &v : accounts) {
        const QVariantMap m = v.toMap();
        if (m.value(QStringLiteral("id")).toString() == m_accountId)
            return m.value(QStringLiteral("email")).toString();
    }
    return {};
}

QString Composer::signatureBlock() const
{
    const auto accounts = Rpc::instance()->accounts();
    for (const QVariant &v : accounts) {
        const QVariantMap m = v.toMap();
        if (m.value(QStringLiteral("id")).toString() == m_accountId) {
            const QString sig = m.value(QStringLiteral("signature")).toString().trimmed();
            if (sig.isEmpty())
                return {};
            if (sig.startsWith(QStringLiteral("--")))
                return QStringLiteral("\n\n") + sig;
            return QStringLiteral("\n\n-- \n") + sig;
        }
    }
    return {};
}

QString Composer::fromText() const
{
    const auto accounts = Rpc::instance()->accounts();
    for (const QVariant &v : accounts) {
        const QVariantMap m = v.toMap();
        if (m.value(QStringLiteral("id")).toString() == m_accountId)
            return senderText(m.value(QStringLiteral("name")).toString(),
                              m.value(QStringLiteral("email")).toString());
    }
    return {};
}

// ---- property setters ---------------------------------------------------------------------------

void Composer::setAccountId(const QString &v)
{
    if (v == m_accountId)
        return;
    m_accountId = v;
    emit accountIdChanged();
    emit fromTextChanged();
}

#define MAILAND_SETTER(Name, member, sig)       \
    void Composer::Name(const QString &v)       \
    {                                           \
        if (v == member)                        \
            return;                             \
        member = v;                             \
        emit sig();                             \
        updateDirty();                          \
    }
MAILAND_SETTER(setTo, m_to, toChanged)
MAILAND_SETTER(setCc, m_cc, ccChanged)
MAILAND_SETTER(setBcc, m_bcc, bccChanged)
MAILAND_SETTER(setSubject, m_subject, subjectChanged)
MAILAND_SETTER(setBody, m_body, bodyChanged)
#undef MAILAND_SETTER

void Composer::setHtmlBody(const QString &v)
{
    if (v == m_htmlBody)
        return;
    m_htmlBody = v;
    emit htmlBodyChanged();
}

void Composer::setSignatureEnabled(bool on)
{
    if (on == m_signatureEnabled)
        return;
    m_signatureEnabled = on;
    emit signatureEnabledChanged();
    const QString sig = signatureBlock();
    if (sig.isEmpty())
        return;
    if (on) {
        if (!m_body.contains(sig))
            setBody(m_body + sig);
    } else {
        QString b = m_body;
        const int idx = b.indexOf(sig);
        if (idx >= 0) {
            b.remove(idx, sig.size());
            setBody(b);
        }
    }
}

void Composer::snapshot()
{
    m_i_to = m_to; m_i_cc = m_cc; m_i_bcc = m_bcc; m_i_subject = m_subject; m_i_body = m_body;
    updateDirty();
}

void Composer::updateDirty()
{
    const bool d = !m_loading
                   && (m_to != m_i_to || m_cc != m_i_cc || m_bcc != m_i_bcc || m_subject != m_i_subject
                       || m_body != m_i_body || !m_userAttachments.isEmpty());
    if (d != m_dirty) {
        m_dirty = d;
        emit dirtyChanged();
    }
}

void Composer::setError(const QString &e)
{
    if (e == m_error)
        return;
    m_error = e;
    emit errorTextChanged();
}

void Composer::setLoading(bool l)
{
    if (l == m_loading)
        return;
    m_loading = l;
    emit loadingChanged();
    updateDirty();
}

// ---- init ---------------------------------------------------------------------------------------

void Composer::reset(const QString &mode, const QString &accountId)
{
    ++m_gen; // invalidates any in-flight init
    if (m_sending) {
        m_sending = false;
        emit sendingChanged();
    }
    setLoading(false);
    setError(QString());
    m_replyToId.clear();
    m_forwardOfId.clear();
    m_forwardAttachmentBytes = 0;
    if (!m_attachments.isEmpty()) {
        m_attachments.clear();
        emit forwardAttachmentsChanged();
    }
    if (!m_userAttachments.isEmpty()) {
        m_userAttachments.clear();
        emit attachmentsChanged();
    }
    setAccountId(accountId);
    if (m_mode != mode) {
        m_mode = mode;
        emit modeChanged();
    }
    setTo({}); setCc({}); setBcc({}); setSubject({}); setBody({}); setHtmlBody({});
    if (!m_signatureEnabled) {
        m_signatureEnabled = true;
        emit signatureEnabledChanged();
    }
    snapshot();
}

void Composer::initNew(const QString &accountId)
{
    reset(QStringLiteral("new"), accountId);
    if (const QString sig = signatureBlock(); !sig.isEmpty()) {
        setBody(sig);
        snapshot();
    }
    emit ready();
}

void Composer::initNewTo(const QString &accountId, const QString &to)
{
    reset(QStringLiteral("new"), accountId);
    setTo(to);
    if (const QString sig = signatureBlock(); !sig.isEmpty())
        setBody(sig);
    snapshot();
    emit ready();
}

void Composer::initReply(const QString &messageId, bool replyAll)
{
    reset(replyAll ? QStringLiteral("replyAll") : QStringLiteral("reply"), m_accountId);
    m_replyToId = messageId;
    loadOriginal(messageId, false, replyAll);
}

void Composer::initForward(const QString &messageId)
{
    reset(QStringLiteral("forward"), m_accountId);
    m_forwardOfId = messageId;
    loadOriginal(messageId, true, false);
}

void Composer::loadOriginal(const QString &messageId, bool forward, bool replyAll)
{
    const int gen = m_gen;
    setLoading(true);
    Rpc::instance()->getMessage(
        messageId, this, [this, gen, forward, replyAll](const grpc::Status &st, const mailand::v1::MessageDetail &d) {
            if (gen != m_gen)
                return;
            setLoading(false);
            if (!st.ok()) {
                const QString msg = QStringLiteral("Could not load the original message: ") + Rpc::statusText(st);
                setError(msg);
                emit failed(msg);
                return;
            }
            const auto &h = d.header();
            const QString subj = s(h.subject());
            const QString fromName = s(h.from_name()), fromAddr = s(h.from_address());
            const QString sender = senderText(fromName, fromAddr);
            const QString date = Format::longDate(h.timestamp_unix());
            const QString orig = s(d.text_body());

            if (forward) {
                QStringList to;
                for (const auto &a : d.to_addresses())
                    to << s(a);
                setSubject(forwardSubject(subj));
                setBody(signatureBlock()
                        + QStringLiteral("\n\n---------- Forwarded message ----------\nFrom: %1\nDate: %2\nSubject: %3\nTo: %4\n\n")
                              .arg(sender, date, subj, to.join(QStringLiteral(", ")))
                        + orig);
                for (const auto &a : d.attachments()) {
                    if (a.inline_())
                        continue;
                    m_forwardAttachmentBytes += static_cast<qint64>(a.size_bytes());
                    m_attachments.append(QVariantMap{{QStringLiteral("filename"), s(a.filename())},
                                                     {QStringLiteral("sizeText"), Format::sizeText(a.size_bytes())}});
                }
                emit forwardAttachmentsChanged();
            } else {
                setSubject(replySubject(subj));
                setTo(sender);
                if (replyAll) {
                    const QString own = ownEmail().trimmed().toLower();
                    QSet<QString> seen;
                    if (!own.isEmpty())
                        seen.insert(own);
                    seen.insert(bareAddress(fromAddr));
                    QStringList toExtra, ccList;
                    for (const auto &a : d.to_addresses()) {
                        const QString b = bareAddress(s(a));
                        if (!b.isEmpty() && !seen.contains(b)) {
                            seen.insert(b);
                            toExtra << s(a);
                        }
                    }
                    for (const auto &a : d.cc_addresses()) {
                        const QString b = bareAddress(s(a));
                        if (!b.isEmpty() && !seen.contains(b)) {
                            seen.insert(b);
                            ccList << s(a);
                        }
                    }
                    // Replying to one's own message: do not address oneself.
                    if (!own.isEmpty() && bareAddress(fromAddr) == own) {
                        setTo(toExtra.join(QStringLiteral(", ")));
                    } else if (!toExtra.isEmpty()) {
                        setTo(sender + QStringLiteral(", ") + toExtra.join(QStringLiteral(", ")));
                    }
                    setCc(ccList.join(QStringLiteral(", ")));
                }
                setBody(signatureBlock() + QStringLiteral("\n\nOn %1, %2 wrote:\n").arg(date, sender) + quoteText(orig) + QLatin1Char('\n'));
            }
            snapshot();
            emit ready();
        });
}

// ---- attachments --------------------------------------------------------------------------------

void Composer::addAttachments(const QList<QUrl> &urls)
{
    if (m_sending || m_loading || urls.isEmpty())
        return;

    qint64 total = m_forwardAttachmentBytes;
    QSet<QString> existing;
    for (const QVariant &v : m_userAttachments) {
        const QVariantMap m = v.toMap();
        existing.insert(m.value(QStringLiteral("path")).toString());
        total += m.value(QStringLiteral("sizeBytes")).toLongLong();
    }

    QMimeDatabase mimeDb;
    bool added = false;
    QString err;
    for (const QUrl &u : urls) {
        const QString path = u.isLocalFile() ? u.toLocalFile() : u.toString();
        if (path.isEmpty() || existing.contains(path))
            continue;
        QFileInfo fi(path);
        if (!fi.exists() || !fi.isFile() || !fi.isReadable()) {
            err = QStringLiteral("Cannot read file: ") + fi.fileName();
            continue;
        }
        const qint64 sz = fi.size();
        if (total + sz > kMaxAttachmentTotal) {
            err = QStringLiteral("Attachments exceed the 25 MiB limit.");
            continue;
        }
        total += sz;
        existing.insert(path);
        QString mime = mimeDb.mimeTypeForFile(fi).name();
        if (mime.isEmpty())
            mime = QStringLiteral("application/octet-stream");
        m_userAttachments.append(QVariantMap{
            {QStringLiteral("path"), path},
            {QStringLiteral("filename"), fi.fileName()},
            {QStringLiteral("mimeType"), mime},
            {QStringLiteral("sizeBytes"), sz},
            {QStringLiteral("sizeText"), Format::sizeText(static_cast<quint64>(sz))},
        });
        added = true;
    }
    setError(err);
    if (added) {
        emit attachmentsChanged();
        updateDirty();
    }
}

void Composer::removeAttachment(int index)
{
    if (m_sending || index < 0 || index >= m_userAttachments.size())
        return;
    m_userAttachments.removeAt(index);
    emit attachmentsChanged();
    updateDirty();
}

// ---- send ---------------------------------------------------------------------------------------

void Composer::send()
{
    if (m_sending || m_loading)
        return;
    const QStringList to = splitAddresses(m_to), cc = splitAddresses(m_cc), bcc = splitAddresses(m_bcc);
    if (to.isEmpty() && cc.isEmpty() && bcc.isEmpty()) {
        const QString msg = QStringLiteral("Add at least one recipient.");
        setError(msg);
        emit failed(msg);
        return;
    }
    if (m_accountId.isEmpty()) {
        const QString msg = QStringLiteral("No account selected.");
        setError(msg);
        emit failed(msg);
        return;
    }

    mailand::v1::SendMessageRequest req;
    req.set_account_id(m_accountId.toStdString());
    for (const QString &a : to) req.add_to(a.toStdString());
    for (const QString &a : cc) req.add_cc(a.toStdString());
    for (const QString &a : bcc) req.add_bcc(a.toStdString());
    req.set_subject(m_subject.toStdString());
    req.set_text_body(m_body.toStdString());
    if (!m_htmlBody.isEmpty())
        req.set_html_body(m_htmlBody.toStdString());
    req.set_reply_to_id(m_replyToId.toStdString());
    req.set_forward_of_id(m_forwardOfId.toStdString());

    qint64 totalBytes = m_forwardAttachmentBytes;
    for (const QVariant &v : m_userAttachments) {
        const QVariantMap m = v.toMap();
        const QString path = m.value(QStringLiteral("path")).toString();
        const QString filename = m.value(QStringLiteral("filename")).toString();
        const QString mimeType = m.value(QStringLiteral("mimeType")).toString();
        QFile f(path);
        if (!f.open(QIODevice::ReadOnly)) {
            const QString msg = QStringLiteral("Could not read attachment: ") + filename;
            setError(msg);
            emit failed(msg);
            return;
        }
        const QByteArray bytes = f.readAll();
        totalBytes += bytes.size();
        if (totalBytes > kMaxAttachmentTotal) {
            const QString msg = QStringLiteral("Attachments exceed the 25 MiB limit.");
            setError(msg);
            emit failed(msg);
            return;
        }
        auto *att = req.add_attachments();
        att->set_filename(filename.toStdString());
        att->set_mime_type(mimeType.toStdString());
        att->set_data(bytes.constData(), static_cast<size_t>(bytes.size()));
    }

    setError(QString());
    m_sending = true;
    emit sendingChanged();
    const int gen = m_gen;
    Rpc::instance()->sendMessage(std::move(req), this, [this, gen](const grpc::Status &st, const mailand::v1::SendMessageResponse &) {
        if (gen != m_gen)
            return;
        m_sending = false;
        emit sendingChanged();
        if (!st.ok()) {
            QString msg = s(st.error_message());
            if (st.error_code() == grpc::StatusCode::UNAUTHENTICATED)
                msg = QStringLiteral("Account needs re-authentication. Run: mailand-daemon login ") + m_accountId;
            else if (st.error_code() == grpc::StatusCode::UNAVAILABLE)
                msg = QStringLiteral("Could not reach the mail server: ") + msg;
            else if (msg.isEmpty())
                msg = Rpc::statusText(st);
            setError(msg);
            emit failed(msg);
            return;
        }
        emit sent();
    });
}
