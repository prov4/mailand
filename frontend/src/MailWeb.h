#pragma once

#include <QHash>
#include <QObject>
#include <QUrl>
#include <QtQml/qqmlregistration.h>

#include <atomic>

class QJSEngine;
class QQmlEngine;
#include <QtWebEngineQuick/QQuickWebEngineProfile>
class QWebEngineUrlRequestInterceptor;
class QWebEngineUrlSchemeHandler;

// Locked-down web profile for rendering mail HTML: custom schemes, request interceptor, CSP.
class MailWeb : public QObject
{
    Q_OBJECT
    QML_ELEMENT
    QML_SINGLETON
    Q_PROPERTY(QQuickWebEngineProfile *profile READ profile CONSTANT FINAL)
    Q_PROPERTY(bool remoteAllowed READ remoteAllowed WRITE setRemoteAllowed NOTIFY remoteAllowedChanged FINAL)

public:
    struct CidPart {
        QString attachmentId;
        QString mimeType;
    };

    // Must run before the QGuiApplication is constructed.
    static void registerSchemes();

    static MailWeb *instance();
    static MailWeb *create(QQmlEngine *, QJSEngine *);

    ~MailWeb() override;

    QQuickWebEngineProfile *profile() const { return m_profile; }
    bool remoteAllowed() const { return m_remoteAllowed.load(); }
    void setRemoteAllowed(bool allowed);

    Q_INVOKABLE void openExternal(const QUrl &url);

    // Used by the scheme handlers / interceptor (GUI thread, except where noted).
    QString contentSecurityPolicy() const;
    void rememberMessage(const QString &messageId, const QHash<QString, CidPart> &cids);
    QString lastMessageId() const { return m_lastMessage; }
    bool cidLookup(const QString &messageId, const QString &cid, CidPart *out) const;
    // Interceptor side (may run off the GUI thread).
    void pageLoadStarted() { m_notified.store(false); }
    void blockedRemote();

signals:
    void remoteAllowedChanged();
    void remoteContentBlocked();

private:
    explicit MailWeb(QObject *parent);

    QQuickWebEngineProfile *m_profile = nullptr;
    QWebEngineUrlRequestInterceptor *m_interceptor = nullptr;
    QWebEngineUrlSchemeHandler *m_msgHandler = nullptr;
    QWebEngineUrlSchemeHandler *m_cidHandler = nullptr;
    std::atomic<bool> m_remoteAllowed{false};
    std::atomic<bool> m_notified{false};
    QString m_lastMessage;
    QHash<QString, QHash<QString, CidPart>> m_cids;
};
