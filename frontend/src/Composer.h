#pragma once

#include <QList>
#include <QObject>
#include <QString>
#include <QUrl>
#include <QVariantList>
#include <QtQml/qqmlregistration.h>

// State + logic of the mail composer. QML binds the fields; reply/forward prefill is async
// (GetMessage) and guarded by a generation counter so a stale answer never overwrites a newer init.
class Composer : public QObject
{
    Q_OBJECT
    QML_ELEMENT
    Q_PROPERTY(QString accountId READ accountId WRITE setAccountId NOTIFY accountIdChanged FINAL)
    Q_PROPERTY(QString to READ to WRITE setTo NOTIFY toChanged FINAL)
    Q_PROPERTY(QString cc READ cc WRITE setCc NOTIFY ccChanged FINAL)
    Q_PROPERTY(QString bcc READ bcc WRITE setBcc NOTIFY bccChanged FINAL)
    Q_PROPERTY(QString subject READ subject WRITE setSubject NOTIFY subjectChanged FINAL)
    Q_PROPERTY(QString body READ body WRITE setBody NOTIFY bodyChanged FINAL)
    Q_PROPERTY(QString htmlBody READ htmlBody WRITE setHtmlBody NOTIFY htmlBodyChanged FINAL)
    Q_PROPERTY(bool hasSignature READ hasSignature NOTIFY fromTextChanged FINAL)
    Q_PROPERTY(bool signatureEnabled READ signatureEnabled WRITE setSignatureEnabled NOTIFY signatureEnabledChanged FINAL)
    Q_PROPERTY(QString mode READ mode NOTIFY modeChanged FINAL)
    Q_PROPERTY(bool sending READ sending NOTIFY sendingChanged FINAL)
    Q_PROPERTY(bool loading READ loading NOTIFY loadingChanged FINAL)
    Q_PROPERTY(bool dirty READ dirty NOTIFY dirtyChanged FINAL)
    Q_PROPERTY(QString errorText READ errorText NOTIFY errorTextChanged FINAL)
    Q_PROPERTY(QVariant forwardAttachments READ forwardAttachments NOTIFY forwardAttachmentsChanged FINAL)
    Q_PROPERTY(QVariant attachments READ attachments NOTIFY attachmentsChanged FINAL)
    Q_PROPERTY(QString fromText READ fromText NOTIFY fromTextChanged FINAL)

public:
    explicit Composer(QObject *parent = nullptr);

    QString accountId() const { return m_accountId; }
    QString to() const { return m_to; }
    QString cc() const { return m_cc; }
    QString bcc() const { return m_bcc; }
    QString subject() const { return m_subject; }
    QString body() const { return m_body; }
    QString htmlBody() const { return m_htmlBody; }
    bool hasSignature() const { return !signatureBlock().isEmpty(); }
    bool signatureEnabled() const { return m_signatureEnabled; }
    QString mode() const { return m_mode; }
    bool sending() const { return m_sending; }
    bool loading() const { return m_loading; }
    bool dirty() const { return m_dirty; }
    QString errorText() const { return m_error; }
    QVariant forwardAttachments() const { return m_attachments; }
    QVariant attachments() const { return m_userAttachments; }
    QString fromText() const;

    void setAccountId(const QString &v);
    void setTo(const QString &v);
    void setCc(const QString &v);
    void setBcc(const QString &v);
    void setSubject(const QString &v);
    void setBody(const QString &v);
    void setHtmlBody(const QString &v);
    void setSignatureEnabled(bool on);

    Q_INVOKABLE void initNew(const QString &accountId);
    // New message with To prefilled (from Contacts); not dirty until edited.
    Q_INVOKABLE void initNewTo(const QString &accountId, const QString &to);
    Q_INVOKABLE void initReply(const QString &messageId, bool replyAll);
    Q_INVOKABLE void initForward(const QString &messageId);
    Q_INVOKABLE void addAttachments(const QList<QUrl> &urls);
    Q_INVOKABLE void removeAttachment(int index);
    Q_INVOKABLE void send();

    // Pure helpers (also used by the selftest).
    static QStringList splitAddresses(const QString &list);
    static QString bareAddress(const QString &addr);
    static QString replySubject(const QString &s);
    static QString forwardSubject(const QString &s);
    static QString quoteText(const QString &text);

signals:
    void accountIdChanged();
    void toChanged();
    void ccChanged();
    void bccChanged();
    void subjectChanged();
    void bodyChanged();
    void htmlBodyChanged();
    void signatureEnabledChanged();
    void modeChanged();
    void sendingChanged();
    void loadingChanged();
    void dirtyChanged();
    void errorTextChanged();
    void forwardAttachmentsChanged();
    void attachmentsChanged();
    void fromTextChanged();
    void ready();
    void sent();
    void failed(const QString &message);

private:
    void reset(const QString &mode, const QString &accountId);
    void snapshot();
    void updateDirty();
    void setError(const QString &e);
    void setLoading(bool l);
    void loadOriginal(const QString &messageId, bool forward, bool replyAll);
    QString ownEmail() const;
    QString signatureBlock() const;

    QString m_accountId, m_to, m_cc, m_bcc, m_subject, m_body, m_htmlBody, m_mode = QStringLiteral("new");
    QString m_error, m_replyToId, m_forwardOfId;
    QString m_i_to, m_i_cc, m_i_bcc, m_i_subject, m_i_body; // snapshot at init
    QVariantList m_attachments;
    QVariantList m_userAttachments;
    qint64 m_forwardAttachmentBytes = 0;
    bool m_signatureEnabled = true;
    bool m_sending = false, m_loading = false, m_dirty = false;
    int m_gen = 0;
};
