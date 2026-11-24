#include "SelfTest.h"

#include <QCoreApplication>
#include <QElapsedTimer>
#include <QEventLoop>
#include <QRegularExpression>
#include <QStringList>
#include <QTextStream>
#include <QTimer>

#include <functional>

#include "FolderModel.h"
#include "MessageModel.h"
#include "MessageView.h"
#include "Rpc.h"

namespace {

QElapsedTimer g_clock;
constexpr int kBudgetMs = 10000;

bool waitUntil(const std::function<bool()> &cond)
{
    while (!cond()) {
        if (g_clock.elapsed() > kBudgetMs)
            return false;
        QEventLoop loop;
        QTimer::singleShot(10, &loop, &QEventLoop::quit);
        loop.exec();
    }
    return true;
}

void settle(int ms)
{
    QEventLoop loop;
    QTimer::singleShot(ms, &loop, &QEventLoop::quit);
    loop.exec();
}

} // namespace

int runSelfTest(int argc, char **argv)
{
    QCoreApplication app(argc, argv);
    g_clock.start();
    QTextStream out(stdout);
    int failures = 0;
    auto check = [&](bool ok, const QString &what) {
        out << (ok ? "ok    " : "FAIL  ") << what << Qt::endl;
        if (!ok)
            ++failures;
        return ok;
    };
    auto finish = [&]() {
        out << (failures ? "selftest: FAILED" : "selftest: PASSED") << " (" << g_clock.elapsed() << " ms)" << Qt::endl;
        return failures ? 1 : 0;
    };

    Rpc *rpc = Rpc::instance();
    QStringList errors;
    QObject::connect(rpc, &Rpc::errorOccurred, &app, [&](const QString &m) { errors << m; });

    // accounts
    rpc->refreshAccounts();
    if (!check(waitUntil([&] { return !rpc->accounts().isEmpty(); }),
               QStringLiteral("accounts loaded (socket %1)").arg(Rpc::socketPath())))
        return finish();
    const QVariantMap acc = rpc->accounts().first().toMap();
    const QString accountId = acc.value("id").toString();
    out << "      account: " << accountId << " " << acc.value("email").toString()
        << " (connected=" << rpc->connected() << ")" << Qt::endl;

    // folders
    FolderModel folders;
    folders.setAccountId(accountId);
    if (!check(waitUntil([&] { return folders.count() > 0 && !folders.loading(); }), "folders loaded"))
        return finish();
    QStringList names;
    for (int i = 0; i < folders.count(); ++i)
        names << QStringLiteral("%1[%2]").arg(folders.data(folders.index(i), FolderModel::DisplayNameRole).toString(),
                                              folders.data(folders.index(i), FolderModel::UnreadCountRole).toString());
    out << "      folders(" << folders.count() << "): " << names.join(", ") << Qt::endl;
    int inboxRow = -1;
    for (int i = 0; i < folders.count(); ++i)
        if (folders.data(folders.index(i), FolderModel::DisplayNameRole).toString() == "Inbox")
            inboxRow = i;
    if (!check(inboxRow >= 0, "Inbox found (sorted first: " + QString::number(inboxRow) + ")"))
        return finish();
    const QString inboxId = folders.folderIdAt(inboxRow);

    // messages
    MessageModel msgs;
    msgs.setAccountId(accountId);
    msgs.setFolderId(inboxId);
    if (!check(waitUntil([&] { return msgs.count() > 0 && !msgs.loading(); }), "inbox first page loaded"))
        return finish();
    const QVariantMap first = msgs.get(0).toMap();
    out << "      inbox: " << msgs.count() << " rows of " << msgs.totalCount() << "; row0: "
        << first.value("dateText").toString() << " | " << first.value("sender").toString() << " | "
        << first.value("subject").toString() << Qt::endl;
    check(msgs.totalCount() >= msgs.count(), "totalCount >= count");

    if (msgs.totalCount() > msgs.count()) {
        const int before = msgs.count();
        check(msgs.canFetchMore({}), "canFetchMore");
        msgs.fetchMore({});
        waitUntil([&] { return !msgs.loading(); });
        check(msgs.count() > before, QStringLiteral("fetchMore appended (%1 -> %2 of %3)").arg(before).arg(msgs.count()).arg(msgs.totalCount()));
    }

    // flag toggle + revert, verified through the daemon with a refresh
    const QString firstId = first.value("messageId").toString();
    const bool flagged0 = first.value("isFlagged").toBool();
    msgs.toggleFlagged(0);
    check(msgs.get(0).toMap().value("isFlagged").toBool() == !flagged0, "optimistic flag toggle");
    settle(100);
    msgs.refresh();
    waitUntil([&] { return !msgs.loading(); });
    int r = msgs.indexOfMessage(firstId);
    check(r >= 0 && msgs.get(r).toMap().value("isFlagged").toBool() == !flagged0, "flag persisted in daemon");
    msgs.toggleFlagged(r);
    settle(100);
    msgs.refresh();
    waitUntil([&] { return !msgs.loading(); });
    r = msgs.indexOfMessage(firstId);
    check(r >= 0 && msgs.get(r).toMap().value("isFlagged").toBool() == flagged0, "flag restored");

    // open message
    MessageView view;
    view.setMessageId(firstId);
    if (check(waitUntil([&] { return !view.loading(); }), "message loaded") && check(view.valid(), "message valid")) {
        out << "      view: \"" << view.subject() << "\" from " << view.fromName() << " <" << view.fromAddress()
            << "> " << view.dateText() << " hasHtml=" << view.hasHtml() << " url=" << view.htmlUrl().toString()
            << " attachments=" << view.attachments().toList().size() << " textBody=" << view.textBody().size()
            << " chars" << Qt::endl;
    }

    // search: longest word of the subject, falling back to "the"
    QString word = QStringLiteral("the");
    for (const QString &w : first.value("subject").toString().split(QRegularExpression("\\W+"), Qt::SkipEmptyParts))
        if (w.size() > word.size())
            word = w;
    MessageModel search;
    search.setAccountId(accountId);
    search.setSearchQuery(word);
    if (check(waitUntil([&] { return search.count() > 0 && !search.loading(); }),
              "search \"" + word + "\" returned results")) {
        out << "      search: " << search.count() << " hits; first snippetRich: "
            << search.get(0).toMap().value("snippetRich").toString().left(80) << Qt::endl;
    }
    // stale-reply guard: change the query quickly, only the last may win
    search.setSearchQuery(QStringLiteral("zzzzqqqq"));
    search.setSearchQuery(word);
    waitUntil([&] { return !search.loading(); });
    check(search.count() > 0, "search after rapid query changes still shows the latest query");

    if (!errors.isEmpty())
        out << "      errors: " << errors.join(" | ") << Qt::endl;
    check(errors.isEmpty(), "no Rpc.errorOccurred");
    return finish();
}
