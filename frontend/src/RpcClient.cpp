#include "RpcClient.h"

#include <QDir>
#include <QMetaObject>
#include <QPointer>
#include <QVariantMap>
#include <QtGlobal>

#include <grpcpp/grpcpp.h>
#include <unistd.h>

#include <chrono>

#include "mailand.grpc.pb.h"

namespace {

using mailand::v1::AccountList;
using mailand::v1::Empty;
using mailand::v1::MailandService;

constexpr auto kCallDeadline = std::chrono::seconds(3);

std::shared_ptr<grpc::Channel> makeChannel()
{
    const std::string target = "unix:" + RpcClient::socketPath().toStdString();
    return grpc::CreateChannel(target, grpc::InsecureChannelCredentials());
}

QString statusText(const grpc::Status &s)
{
    return QString::fromStdString(s.error_message().empty() ? "gRPC error" : s.error_message())
           + QStringLiteral(" (code %1)").arg(static_cast<int>(s.error_code()));
}

// Everything the in-flight call needs; kept alive by the callback via shared_ptr
// so the RpcClient may be destroyed before the RPC completes.
struct Call {
    std::unique_ptr<MailandService::Stub> stub; // must outlive the in-flight call
    grpc::ClientContext ctx;
    Empty request;
    AccountList response;
};

} // namespace

RpcClient::RpcClient(QObject *parent)
    : QObject(parent), m_channel(makeChannel())
{
}

QString RpcClient::socketPath()
{
    if (const QByteArray p = qgetenv("MAILAND_SOCKET"); !p.isEmpty())
        return QString::fromLocal8Bit(p);
    if (const QByteArray d = qgetenv("XDG_RUNTIME_DIR"); !d.isEmpty())
        return QDir(QString::fromLocal8Bit(d)).filePath(QStringLiteral("mailand.sock"));
    return QStringLiteral("/tmp/mailand-%1.sock").arg(getuid());
}

void RpcClient::refreshAccounts()
{
    auto call = std::make_shared<Call>();
    call->stub = MailandService::NewStub(m_channel);
    call->ctx.set_deadline(std::chrono::system_clock::now() + kCallDeadline);
    // Wait for the daemon to come up instead of failing immediately on a cold channel.
    call->ctx.set_wait_for_ready(true);

    QPointer<RpcClient> self(this);
    call->stub->async()->ListAccounts(
        &call->ctx, &call->request, &call->response,
        [self, call](grpc::Status status) {
            bool ok = status.ok();
            QString error = ok ? QString() : statusText(status);
            QVariantList list;
            if (ok) {
                for (const auto &a : call->response.accounts())
                    list.append(QVariantMap{
                        {QStringLiteral("id"), QString::fromStdString(a.id())},
                        {QStringLiteral("name"), QString::fromStdString(a.name())},
                        {QStringLiteral("email"), QString::fromStdString(a.email())},
                    });
            }
            // Runs on a gRPC thread: hop to the object's (Qt) thread. No-op if destroyed.
            if (!self)
                return;
            QMetaObject::invokeMethod(
                self.data(),
                [self, ok, error, list] {
                    if (self)
                        self->handleResult(ok, error, list);
                },
                Qt::QueuedConnection);
        });
}

void RpcClient::handleResult(bool ok, const QString &error, const QVariantList &accounts)
{
    if (ok) {
        m_accounts = accounts;
        emit accountsChanged();
    }
    if (m_connected != ok) {
        m_connected = ok;
        emit connectedChanged();
    }
    if (m_lastError != error) {
        m_lastError = error;
        emit lastErrorChanged();
    }
}

bool RpcClient::listAccountsSync(QList<AccountInfo> *out, QString *error, int timeoutMs)
{
    auto stub = MailandService::NewStub(makeChannel());
    grpc::ClientContext ctx;
    ctx.set_deadline(std::chrono::system_clock::now() + std::chrono::milliseconds(timeoutMs));
    Empty req;
    AccountList resp;
    const grpc::Status status = stub->ListAccounts(&ctx, req, &resp);
    if (!status.ok()) {
        if (error)
            *error = QStringLiteral("ListAccounts via %1 failed: %2")
                         .arg(socketPath(), statusText(status));
        return false;
    }
    for (const auto &a : resp.accounts())
        out->append({QString::fromStdString(a.id()), QString::fromStdString(a.name()),
                     QString::fromStdString(a.email())});
    return true;
}
