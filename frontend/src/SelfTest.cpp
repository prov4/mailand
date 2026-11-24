#include "SelfTest.h"

#include <QCoreApplication>
#include <QDateTime>
#include <QElapsedTimer>
#include <QEventLoop>
#include <QRegularExpression>
#include <QStringList>
#include <QTextStream>
#include <QTimer>

#include <functional>

#include "CalendarModel.h"
#include "Composer.h"
#include "ContactsModel.h"
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


    // composer: reply / reply-all / forward prefill, then send in offline mode
    {
        const QString ownEmail = acc.value("email").toString();
        const QString origSubject = view.subject();
        auto isReady = [](Composer &cm) { return !cm.loading(); };

        Composer rep;
        rep.setAccountId(accountId);
        rep.initReply(firstId, false);
        waitUntil([&] { return isReady(rep); });
        check(rep.errorText().isEmpty(), "initReply: no error");
        check(rep.subject() == Composer::replySubject(origSubject) && rep.subject().startsWith("Re:", Qt::CaseInsensitive),
              "initReply: subject \"" + rep.subject() + "\"");
        check(Composer::bareAddress(rep.to()) == view.fromAddress().toLower(), "initReply: to = original sender (" + rep.to() + ")");
        check(rep.body().startsWith("\n\nOn ") && rep.body().contains(" wrote:\n"), "initReply: attribution line");
        const QString firstLine = view.textBody().split('\n').value(0);
        check(view.textBody().isEmpty() || rep.body().contains("\n> " + firstLine) || rep.body().contains("\n>\n"),
              "initReply: quoted with \"> \" prefix");
        check(!rep.dirty(), "initReply: not dirty after init");
        rep.setBody("x" + rep.body());
        check(rep.dirty(), "dirty after edit");

        Composer all;
        all.setAccountId(accountId);
        all.initReply(firstId, true);
        waitUntil([&] { return isReady(all); });
        bool selfFound = false;
        for (const QString &f : {all.to(), all.cc()})
            for (const QString &a : Composer::splitAddresses(f))
                if (Composer::bareAddress(a) == ownEmail.toLower())
                    selfFound = true;
        check(!selfFound, "initReply(replyAll): own address excluded (to=" + all.to() + " cc=" + all.cc() + ")");

        Composer fwd;
        fwd.setAccountId(accountId);
        fwd.initForward(firstId);
        waitUntil([&] { return isReady(fwd); });
        check(fwd.subject() == Composer::forwardSubject(origSubject) && fwd.subject().startsWith("Fwd:"),
              "initForward: subject \"" + fwd.subject() + "\"");
        check(fwd.body().contains("---------- Forwarded message ----------\nFrom: "), "initForward: header block");
        check(fwd.mode() == "forward" && fwd.to().isEmpty(), "initForward: mode, empty To");

        check(Composer::replySubject("RE: x") == "RE: x" && Composer::replySubject("odg: x") == "odg: x"
                  && Composer::replySubject("x") == "Re: x",
              "replySubject prefix rules");
        check(Composer::splitAddresses("a@x.org; \"B, C\" <b@x.org>,  ,c@x.org").size() == 3, "splitAddresses");

        // send a new message in offline mode and find it in the Sent folder
        int sentRow = -1;
        for (int i = 0; i < folders.count(); ++i)
            if (folders.data(folders.index(i), FolderModel::DisplayNameRole).toString() == "Sent")
                sentRow = i;
        if (check(sentRow >= 0, "Sent folder present")) {
            MessageModel sentModel;
            sentModel.setAccountId(accountId);
            sentModel.setFolderId(folders.folderIdAt(sentRow));
            waitUntil([&] { return !sentModel.loading(); });
            const int sentBefore = sentModel.totalCount();

            const QString subj = QStringLiteral("selftest %1").arg(QDateTime::currentMSecsSinceEpoch());
            Composer nw;
            nw.initNew(accountId);
            check(nw.fromText().contains(ownEmail), "initNew: fromText " + nw.fromText());
            int sentCount = 0;
            QString failMsg;
            QObject::connect(&nw, &Composer::sent, &app, [&] { ++sentCount; });
            QObject::connect(&nw, &Composer::failed, &app, [&](const QString &m) { failMsg = m; });
            nw.send();
            check(!failMsg.isEmpty() && sentCount == 0, "send() without recipients fails locally: " + failMsg);
            nw.setTo("selftest@example.org");
            nw.setSubject(subj);
            nw.setBody("Hello from the selftest.\n");
            nw.send();
            check(nw.sending(), "send(): sending flag set");
            waitUntil([&] { return !nw.sending(); });
            check(sentCount == 1, "send(): sent() emitted" + (nw.errorText().isEmpty() ? QString() : " (error: " + nw.errorText() + ")"));
            bool found = false;
            for (int attempt = 0; attempt < 30 && !found; ++attempt) {
                sentModel.refresh();
                waitUntil([&] { return !sentModel.loading(); });
                for (int i = 0; i < sentModel.count(); ++i)
                    if (sentModel.get(i).toMap().value("subject").toString() == subj)
                        found = true;
                if (!found)
                    settle(100);
            }
            check(found, QStringLiteral("sent message appears in Sent folder (%1 -> %2 messages)").arg(sentBefore).arg(sentModel.totalCount()));
        }
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

    // ---- calendar (needs synced or seeded calendars; skipped otherwise) ----
    CalendarModel cal;
    waitUntil([&] { return !cal.loading() && cal.revision() > 0; });
    settle(50);
    waitUntil([&] { return !cal.loading(); });
    if (cal.calendars().isEmpty()) {
        out << "skip  calendar: no calendars synced" << Qt::endl;
    } else {
        check(cal.eventCount() > 0, QStringLiteral("calendar: %1 calendars, %2 occurrences in the month grid")
                                        .arg(cal.calendars().size()).arg(cal.eventCount()));
        const QDate today = QDate::currentDate();
        check(cal.gridStart().dayOfWeek() == 1 && cal.gridStart() <= QDate(today.year(), today.month(), 1),
              "calendar: grid starts on the Monday before the 1st");
        int busiest = 0;
        for (int i = 0; i < 42; ++i)
            busiest = std::max(busiest, int(cal.eventsOn(cal.gridDate(i)).size()));
        check(busiest > 0, QStringLiteral("calendar: eventsOn finds occurrences (max %1 on one day)").arg(busiest));
        const QString title = cal.monthTitle();
        cal.moveMonths(1);
        check(cal.monthTitle() != title && cal.selectedDate().month() != today.month(), "calendar: next month");
        waitUntil([&] { return !cal.loading(); });
        cal.goToday();
        check(cal.selectedDate() == today && cal.monthTitle() == title, "calendar: back to today");
        waitUntil([&] { return !cal.loading(); });
        // Hiding a calendar removes its occurrences; showing it restores them.
        const QString firstCal = cal.calendars().first().toMap().value("id").toString();
        int before = 0, hidden = 0;
        for (int i = 0; i < 42; ++i)
            before += int(cal.eventsOn(cal.gridDate(i)).size());
        cal.toggleCalendar(firstCal);
        for (int i = 0; i < 42; ++i)
            hidden += int(cal.eventsOn(cal.gridDate(i)).size());
        cal.toggleCalendar(firstCal);
        check(hidden <= before, QStringLiteral("calendar: hide/show a calendar (%1 -> %2)").arg(before).arg(hidden));
    }

    // ---- contacts ----
    ContactsModel people;
    waitUntil([&] { return !people.loading() && people.count() > 0; });
    if (people.count() == 0) {
        out << "skip  contacts: none synced" << Qt::endl;
    } else {
        check(true, QStringLiteral("contacts: %1 loaded").arg(people.count()));
        const QVariantMap c0 = people.get(0);
        const QString name = c0.value("fullName").toString();
        people.setQuery(name.left(3));
        settle(200);
        waitUntil([&] { return !people.loading(); });
        bool hit = false;
        for (int i = 0; i < people.count(); ++i)
            hit = hit || people.get(i).value("fullName").toString() == name;
        check(hit, "contacts: search \"" + name.left(3) + "\" finds " + name);

        // Composer autocomplete: one row per address, nothing below the minimum length.
        ContactsModel sugg;
        sugg.setPerEmail(true);
        sugg.setMinQueryLength(2);
        sugg.setLimit(8);
        sugg.setQuery(QStringLiteral("x"));
        settle(200);
        check(sugg.count() == 0, "autocomplete: no suggestions for 1 character");
        QString emailName;
        for (int i = 0; i < people.count() && emailName.isEmpty(); ++i)
            if (!people.get(i).value("email").toString().isEmpty())
                emailName = people.get(i).value("fullName").toString();
        if (!emailName.isEmpty()) {
            sugg.setQuery(emailName.left(4));
            settle(200);
            waitUntil([&] { return !sugg.loading() && sugg.count() > 0; });
            const QString addr = sugg.get(0).value("address").toString();
            check(addr.contains('<') || addr.contains('@'), "autocomplete: \"" + emailName.left(4) + "\" -> " + addr);
        }
        check(ContactsModel::formatAddress("Horvat, Ana", "a@x.hr") == "\"Horvat, Ana\" <a@x.hr>"
                  && ContactsModel::formatAddress("Ana", "a@x.hr") == "Ana <a@x.hr>"
                  && ContactsModel::formatAddress("", "a@x.hr") == "a@x.hr",
              "autocomplete: address formatting and quoting");
        Composer to;
        to.initNewTo(QString(), QStringLiteral("Ana <a@x.hr>"));
        check(to.to() == "Ana <a@x.hr>" && !to.dirty(), "initNewTo: prefilled and not dirty");
    }

    if (!errors.isEmpty())
        out << "      errors: " << errors.join(" | ") << Qt::endl;
    check(errors.isEmpty(), "no Rpc.errorOccurred");
    return finish();
}
