#pragma once

#include <QObject>
#include <QString>
#include <QUrl>
#include <QVariantList>
#include <QtQml/qqmlregistration.h>

class MessageView : public QObject
{
    Q_OBJECT
    QML_ELEMENT
    Q_PROPERTY(QString messageId READ messageId WRITE setMessageId NOTIFY messageIdChanged FINAL)
    Q_PROPERTY(bool loading READ loading NOTIFY changed FINAL)
    Q_PROPERTY(bool valid READ valid NOTIFY changed FINAL)
    Q_PROPERTY(QString subject READ subject NOTIFY changed FINAL)
    Q_PROPERTY(QString fromName READ fromName NOTIFY changed FINAL)
    Q_PROPERTY(QString fromAddress READ fromAddress NOTIFY changed FINAL)
    Q_PROPERTY(QString dateText READ dateText NOTIFY changed FINAL)
    Q_PROPERTY(QVariant to READ to NOTIFY changed FINAL)
    Q_PROPERTY(QVariant cc READ cc NOTIFY changed FINAL)
    Q_PROPERTY(QString textBody READ textBody NOTIFY changed FINAL)
    Q_PROPERTY(bool hasHtml READ hasHtml NOTIFY changed FINAL)
    Q_PROPERTY(QUrl htmlUrl READ htmlUrl NOTIFY changed FINAL)
    Q_PROPERTY(QVariant attachments READ attachments NOTIFY changed FINAL)
    Q_PROPERTY(QString errorText READ errorText NOTIFY changed FINAL)

public:
    explicit MessageView(QObject *parent = nullptr);

    QString messageId() const { return m_id; }
    void setMessageId(const QString &id);
    bool loading() const { return m_loading; }
    bool valid() const { return m_valid; }
    QString subject() const { return m_subject; }
    QString fromName() const { return m_fromName; }
    QString fromAddress() const { return m_fromAddress; }
    QString dateText() const { return m_dateText; }
    QVariant to() const { return m_to; }
    QVariant cc() const { return m_cc; }
    QString textBody() const { return m_textBody; }
    bool hasHtml() const { return m_hasHtml; }
    QUrl htmlUrl() const { return m_htmlUrl; }
    QVariant attachments() const { return m_attachments; }
    QString errorText() const { return m_error; }

    Q_INVOKABLE bool saveAttachment(const QString &attachmentId, const QUrl &targetFile);

signals:
    void messageIdChanged();
    void changed(); // any of the read-only properties
    void attachmentSaved(const QString &path);

private:
    void clear();

    QString m_id;
    bool m_loading = false, m_valid = false, m_hasHtml = false;
    QString m_subject, m_fromName, m_fromAddress, m_dateText, m_textBody, m_error;
    QVariantList m_to, m_cc, m_attachments;
    QUrl m_htmlUrl;
    int m_gen = 0;
};
