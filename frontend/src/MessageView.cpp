#include "MessageView.h"

#include <QFile>
#include <QSaveFile>

#include "Format.h"
#include "Rpc.h"

MessageView::MessageView(QObject *parent) : QObject(parent) {}

void MessageView::clear()
{
    m_loading = m_valid = m_hasHtml = false;
    m_subject.clear(); m_fromName.clear(); m_fromAddress.clear(); m_dateText.clear();
    m_textBody.clear(); m_error.clear();
    m_to.clear(); m_cc.clear(); m_attachments.clear();
    m_htmlUrl.clear();
}

void MessageView::setMessageId(const QString &id)
{
    if (id == m_id)
        return;
    m_id = id;
    const int gen = ++m_gen;
    clear();
    emit messageIdChanged();
    if (id.isEmpty()) {
        emit changed();
        return;
    }
    m_loading = true;
    emit changed();

    Rpc::instance()->getMessage(id, this, [this, gen, id](const grpc::Status &st, const mailand::v1::MessageDetail &d) {
        if (gen != m_gen)
            return;
        m_loading = false;
        if (!st.ok()) {
            m_error = Rpc::statusText(st);
            Rpc::instance()->reportError(QStringLiteral("Could not open message: ") + m_error);
            emit changed();
            return;
        }
        const auto &h = d.header();
        m_subject = QString::fromStdString(h.subject());
        if (m_subject.isEmpty())
            m_subject = QStringLiteral("(no subject)");
        m_fromName = QString::fromStdString(h.from_name());
        m_fromAddress = QString::fromStdString(h.from_address());
        m_dateText = Format::longDate(h.timestamp_unix());
        for (const auto &a : d.to_addresses())
            m_to.append(QString::fromStdString(a));
        for (const auto &a : d.cc_addresses())
            m_cc.append(QString::fromStdString(a));
        m_textBody = QString::fromStdString(d.text_body());
        m_hasHtml = !d.html_body().empty();
        if (m_hasHtml)
            m_htmlUrl = QUrl(QStringLiteral("mailand-msg://%1/").arg(id));
        for (const auto &a : d.attachments()) {
            if (a.inline_())
                continue;
            m_attachments.append(QVariantMap{
                {QStringLiteral("id"), QString::fromStdString(a.id())},
                {QStringLiteral("filename"), QString::fromStdString(a.filename())},
                {QStringLiteral("mimeType"), QString::fromStdString(a.mime_type())},
                {QStringLiteral("size"), qulonglong(a.size_bytes())},
                {QStringLiteral("sizeText"), Format::sizeText(a.size_bytes())},
            });
        }
        m_valid = true;
        emit changed();
    });
}

bool MessageView::saveAttachment(const QString &attachmentId, const QUrl &targetFile)
{
    // Asynchronous despite the bool: returns true once the download has been started.
    if (m_id.isEmpty() || attachmentId.isEmpty() || targetFile.isEmpty())
        return false;
    const QString path = targetFile.isLocalFile() ? targetFile.toLocalFile() : targetFile.toString();
    Rpc::instance()->getAttachment(
        m_id, attachmentId, this, [this, path](const grpc::Status &st, const mailand::v1::AttachmentData &a) {
            if (!st.ok()) {
                Rpc::instance()->reportError(QStringLiteral("Could not download attachment: ") + Rpc::statusText(st));
                return;
            }
            QSaveFile f(path);
            if (!f.open(QIODevice::WriteOnly)
                || f.write(a.data().data(), qint64(a.data().size())) != qint64(a.data().size())
                || !f.commit()) {
                Rpc::instance()->reportError(QStringLiteral("Could not write %1: %2").arg(path, f.errorString()));
                return;
            }
            QFile::setPermissions(path, QFileDevice::ReadOwner | QFileDevice::WriteOwner
                                            | QFileDevice::ReadGroup | QFileDevice::ReadOther);
            emit attachmentSaved(path);
        });
    return true;
}
