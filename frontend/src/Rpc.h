#pragma once

#include <QList>
#include <QObject>
#include <QHash>
#include <QSet>
#include <QString>
#include <QVariantList>
#include <QtQml/qqmlregistration.h>

#include <condition_variable>
#include <functional>
#include <memory>
#include <mutex>
#include <optional>
#include <thread>

#include <grpcpp/grpcpp.h>

#include "mailand.grpc.pb.h"

class QJSEngine;
class QQmlEngine;

struct AccountInfo {
    QString id;
    QString name;
    QString email;
};

// The one gRPC client of the process. QML sees it as the `Rpc` singleton; the C++ models use
// Rpc::instance() so both talk to the very same object / channel / event stream.
class Rpc : public QObject
{
    Q_OBJECT
    QML_ELEMENT
    QML_SINGLETON
    Q_PROPERTY(bool connected READ connected NOTIFY connectedChanged FINAL)
    Q_PROPERTY(QString lastError READ lastError NOTIFY lastErrorChanged FINAL)
    Q_PROPERTY(QVariantList accounts READ accounts NOTIFY accountsChanged FINAL)
    Q_PROPERTY(bool syncing READ syncing NOTIFY syncingChanged FINAL)
    // Messages still to download across accounts (initial/backlog sync); 0 if unknown.
    Q_PROPERTY(int syncRemaining READ syncRemaining NOTIFY syncingChanged FINAL)
    Q_PROPERTY(QString reauthAccount READ reauthAccount NOTIFY reauthAccountChanged FINAL)

public:
    template <class R>
    using Done = std::function<void(const grpc::Status &, const R &)>;

    // Shared instance (parented to the QCoreApplication; needs one to exist).
    static Rpc *instance();
    // QML_SINGLETON factory: hands out the shared instance, never owned by the JS engine.
    static Rpc *create(QQmlEngine *, QJSEngine *);

    ~Rpc() override;

    bool connected() const { return m_connected; }
    QString lastError() const { return m_lastError; }
    QVariantList accounts() const { return m_accounts; }
    bool syncing() const { return !m_syncing.isEmpty(); }
    int syncRemaining() const;
    QString reauthAccount() const { return m_reauthAccount; }

    // $MAILAND_SOCKET, $XDG_RUNTIME_DIR/mailand.sock, /tmp/mailand-<uid>.sock (same as backend).
    static QString socketPath();
    static QString statusText(const grpc::Status &s);

    Q_INVOKABLE void refreshAccounts();
    Q_INVOKABLE void triggerSync(const QString &accountId);

    // Typed async wrappers. `done` runs on the GUI thread, only if `ctx` (or this) is still alive.
    void listFolders(const QString &accountId, QObject *ctx, Done<mailand::v1::FolderList> done);
    void listMessages(const QString &folderId, quint32 limit, quint32 offset, QObject *ctx,
                      Done<mailand::v1::MessageHeaderList> done);
    void searchMessages(const QString &query, const QString &accountId, quint32 limit,
                        QObject *ctx, Done<mailand::v1::MessageHeaderList> done);
    void getMessage(const QString &messageId, QObject *ctx, Done<mailand::v1::MessageDetail> done);
    void getAttachment(const QString &messageId, const QString &attachmentId, QObject *ctx,
                       Done<mailand::v1::AttachmentData> done);
    void setFlags(const QString &messageId, std::optional<bool> isRead,
                  std::optional<bool> isFlagged, QObject *ctx, Done<mailand::v1::Empty> done);

    void sendMessage(mailand::v1::SendMessageRequest req, QObject *ctx,
                     Done<mailand::v1::SendMessageResponse> done);

    // Surface a user-visible failure (emits errorOccurred).
    void reportError(const QString &message);

    // Blocking, for --print only.
    static bool listAccountsSync(QList<AccountInfo> *out, QString *error, int timeoutMs = 3000);

signals:
    void connectedChanged();
    void lastErrorChanged();
    void accountsChanged();
    void syncingChanged();
    void reauthAccountChanged();
    void daemonEvent(int type, const QString &accountId, const QString &folderId,
                     const QString &detail);
    void errorOccurred(const QString &message);

private:
    explicit Rpc(QObject *parent = nullptr);

    template <class Resp, class Req, class Invoke>
    void call(Req req, Invoke invoke, QObject *ctx, Done<Resp> done, int deadlineMs = 10000,
              bool waitForReady = false);

    void setConnected(bool connected, const QString &error);
    void handleEvent(int type, const QString &accountId, const QString &folderId,
                     const QString &detail);
    void eventLoop();
    void shutdown();

    std::shared_ptr<grpc::Channel> m_channel;
    QVariantList m_accounts;
    bool m_connected = false;
    QString m_lastError;
    QSet<QString> m_syncing;
    QHash<QString, int> m_remaining; // "account\nfolder" -> messages left
    QString m_reauthAccount;

    // Event thread state.
    std::thread m_thread;
    std::mutex m_mutex;
    std::condition_variable m_cv;
    bool m_stopping = false;                  // guarded by m_mutex
    grpc::ClientContext *m_streamCtx = nullptr; // guarded by m_mutex
};
