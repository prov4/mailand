#pragma once

#include <QList>
#include <QObject>
#include <QString>
#include <QVariantList>
#include <QtQml/qqmlregistration.h>

#include <memory>

namespace grpc { class Channel; }

struct AccountInfo {
    QString id;
    QString name;
    QString email;
};

// Thin wrapper around the gRPC channel to mailand-daemon, exposed to QML.
class RpcClient : public QObject
{
    Q_OBJECT
    QML_ELEMENT
    Q_PROPERTY(QVariantList accounts READ accounts NOTIFY accountsChanged FINAL)
    Q_PROPERTY(bool connected READ connected NOTIFY connectedChanged FINAL)
    Q_PROPERTY(QString lastError READ lastError NOTIFY lastErrorChanged FINAL)

public:
    explicit RpcClient(QObject *parent = nullptr);

    QVariantList accounts() const { return m_accounts; }
    bool connected() const { return m_connected; }
    QString lastError() const { return m_lastError; }

    // Same resolution as the backend: $MAILAND_SOCKET, $XDG_RUNTIME_DIR/mailand.sock, /tmp/mailand-<uid>.sock.
    static QString socketPath();

    // Non-blocking; results are delivered on the Qt thread.
    Q_INVOKABLE void refreshAccounts();

    // Blocking, for --print only. Returns false and fills *error on failure.
    static bool listAccountsSync(QList<AccountInfo> *out, QString *error, int timeoutMs = 3000);

signals:
    void accountsChanged();
    void connectedChanged();
    void lastErrorChanged();

private:
    void handleResult(bool ok, const QString &error, const QVariantList &accounts);

    std::shared_ptr<grpc::Channel> m_channel;
    QVariantList m_accounts;
    bool m_connected = false;
    QString m_lastError;
};
