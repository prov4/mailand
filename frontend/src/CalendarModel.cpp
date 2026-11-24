#include "CalendarModel.h"

#include "Rpc.h"
#include "mailand.grpc.pb.h"

#include <QLocale>
#include <QSettings>

#include <algorithm>

namespace {

QString s(const std::string &v) { return QString::fromStdString(v); }

// Used when a server reports no calendar color: distinct on the dark theme.
const char *const kPalette[] = {"#7aa2f7", "#9ece6a", "#e0af68", "#bb9af7", "#7dcfff", "#f7768e", "#73daca", "#ff9e64"};

constexpr int kGridDays = 42;

} // namespace

CalendarModel::CalendarModel(QObject *parent) : QObject(parent)
{
    m_selected = QDate::currentDate();
    m_month = QDate(m_selected.year(), m_selected.month(), 1);

    QSettings settings;
    const QStringList hidden = settings.value(QStringLiteral("calendar/hidden")).toStringList();
    m_hidden = QSet<QString>(hidden.begin(), hidden.end());

    // Coalesce bursts of CALENDARS_UPDATED (one per account) into one reload.
    m_reload.setSingleShot(true);
    m_reload.setInterval(300);
    connect(&m_reload, &QTimer::timeout, this, &CalendarModel::refresh);
    connect(Rpc::instance(), &Rpc::daemonEvent, this, [this](int type, const QString &, const QString &, const QString &) {
        if (type == mailand::v1::DaemonEvent::CALENDARS_UPDATED && !m_reload.isActive())
            m_reload.start();
    });
    connect(Rpc::instance(), &Rpc::connectedChanged, this, [this] {
        if (Rpc::instance()->connected())
            refresh();
    });
    refresh();
}

QDate CalendarModel::gridStart() const
{
    return m_month.addDays(-(m_month.dayOfWeek() - 1)); // back to Monday
}

QString CalendarModel::monthTitle() const
{
    QString name = QLocale().standaloneMonthName(m_month.month());
    if (!name.isEmpty())
        name[0] = name[0].toUpper(); // e.g. Croatian "studeni"
    return name + QLatin1Char(' ') + QString::number(m_month.year());
}

const CalendarModel::Cal *CalendarModel::calendar(const QString &id) const
{
    for (const Cal &c : m_cals)
        if (c.id == id)
            return &c;
    return nullptr;
}

QString CalendarModel::colorOf(const QString &calendarId) const
{
    for (size_t i = 0; i < m_cals.size(); ++i) {
        if (m_cals[i].id == calendarId)
            return m_cals[i].color.isEmpty() ? QString::fromLatin1(kPalette[i % std::size(kPalette)]) : m_cals[i].color;
    }
    return QString::fromLatin1(kPalette[0]);
}

QVariantList CalendarModel::calendars() const
{
    QVariantList out;
    for (const Cal &c : m_cals) {
        QString account;
        for (const QVariant &a : Rpc::instance()->accounts()) {
            const QVariantMap m = a.toMap();
            if (m.value(QStringLiteral("id")).toString() == c.accountId)
                account = m.value(QStringLiteral("name")).toString();
        }
        out.append(QVariantMap{{QStringLiteral("id"), c.id},
                               {QStringLiteral("name"), c.name},
                               {QStringLiteral("accountName"), account},
                               {QStringLiteral("color"), colorOf(c.id)},
                               {QStringLiteral("visible"), !m_hidden.contains(c.id)}});
    }
    return out;
}

void CalendarModel::select(const QDate &day)
{
    if (!day.isValid())
        return;
    if (day != m_selected) {
        m_selected = day;
        emit selectedDateChanged();
    }
    const QDate month(day.year(), day.month(), 1);
    if (month != m_month) {
        m_month = month;
        emit monthChanged();
        loadEvents();
    }
}

void CalendarModel::toggleCalendar(const QString &calendarId)
{
    if (!m_hidden.remove(calendarId))
        m_hidden.insert(calendarId);
    QSettings().setValue(QStringLiteral("calendar/hidden"), QStringList(m_hidden.begin(), m_hidden.end()));
    emit calendarsChanged();
    ++m_revision;
    emit revisionChanged();
}

void CalendarModel::refresh()
{
    loadCalendars();
    loadEvents();
}

void CalendarModel::setPending(int delta)
{
    const bool was = loading();
    m_pending += delta;
    if (was != loading())
        emit loadingChanged();
}

void CalendarModel::loadCalendars()
{
    setPending(1);
    Rpc::instance()->listCalendars(this, [this](const grpc::Status &st, const mailand::v1::CalendarList &resp) {
        setPending(-1);
        if (!st.ok())
            return;
        std::vector<Cal> cals;
        for (const auto &c : resp.calendars())
            cals.push_back({s(c.id()), s(c.account_id()), s(c.name()), s(c.color()), c.read_only()});
        m_cals = std::move(cals);
        emit calendarsChanged();
        ++m_revision; // colors may have changed
        emit revisionChanged();
    });
}

void CalendarModel::loadEvents()
{
    const QDate first = gridStart();
    const qint64 from = first.startOfDay().toSecsSinceEpoch();
    const qint64 to = first.addDays(kGridDays).startOfDay().toSecsSinceEpoch();
    const int gen = ++m_gen;
    setPending(1);
    Rpc::instance()->listEvents(from, to, this, [this, gen](const grpc::Status &st, const mailand::v1::EventList &resp) {
        setPending(-1);
        if (gen != m_gen) // a newer month was requested meanwhile
            return;
        if (!st.ok()) {
            if (st.error_code() != grpc::StatusCode::UNAVAILABLE)
                Rpc::instance()->reportError(QStringLiteral("Calendar: ") + Rpc::statusText(st));
            return;
        }
        std::vector<Ev> evs;
        evs.reserve(resp.events_size());
        for (const auto &e : resp.events()) {
            evs.push_back({s(e.id()), s(e.calendar_id()), s(e.summary()), s(e.location()), s(e.description()),
                           s(e.organizer()), s(e.status()), QDateTime::fromSecsSinceEpoch(e.start_unix()),
                           QDateTime::fromSecsSinceEpoch(e.end_unix()), e.all_day(), e.recurring()});
        }
        m_events = std::move(evs);
        ++m_revision;
        emit revisionChanged();
    });
}

QVariantList CalendarModel::eventsOn(const QDate &day, int revision) const
{
    Q_UNUSED(revision);
    const QDateTime dayStart = day.startOfDay();
    const QDateTime dayEnd = day.addDays(1).startOfDay();
    std::vector<const Ev *> hits;
    for (const Ev &e : m_events) {
        if (m_hidden.contains(e.calendarId))
            continue;
        const bool point = e.end <= e.start;
        if (point ? (e.start >= dayStart && e.start < dayEnd) : (e.start < dayEnd && e.end > dayStart))
            hits.push_back(&e);
    }
    std::stable_sort(hits.begin(), hits.end(), [](const Ev *a, const Ev *b) {
        if (a->allDay != b->allDay)
            return a->allDay;
        return a->start < b->start;
    });

    const QLocale loc;
    const QString fmt = loc.timeFormat(QLocale::ShortFormat);
    QVariantList out;
    for (const Ev *e : hits) {
        const bool multiDay = e->allDay ? e->start.date().daysTo(e->end.date()) > 1
                                        : e->start.date() != e->end.addSecs(-1).date();
        QString timeText;
        if (e->allDay) {
            timeText = QStringLiteral("All day");
        } else if (multiDay) {
            timeText = loc.toString(e->start, QStringLiteral("d MMM ") + fmt) + QStringLiteral(" – ")
                       + loc.toString(e->end, QStringLiteral("d MMM ") + fmt);
        } else if (e->end > e->start) {
            timeText = loc.toString(e->start.time(), fmt) + QStringLiteral(" – ") + loc.toString(e->end.time(), fmt);
        } else {
            timeText = loc.toString(e->start.time(), fmt);
        }
        const Cal *c = calendar(e->calendarId);
        out.append(QVariantMap{
            {QStringLiteral("id"), e->id},
            {QStringLiteral("summary"), e->summary.isEmpty() ? QStringLiteral("(no title)") : e->summary},
            {QStringLiteral("location"), e->location},
            {QStringLiteral("description"), e->description},
            {QStringLiteral("organizer"), e->organizer},
            {QStringLiteral("status"), e->status},
            {QStringLiteral("start"), e->start},
            {QStringLiteral("end"), e->end},
            {QStringLiteral("allDay"), e->allDay},
            {QStringLiteral("recurring"), e->recurring},
            {QStringLiteral("multiDay"), multiDay},
            {QStringLiteral("startText"), e->allDay ? QString() : loc.toString(e->start.time(), fmt)},
            {QStringLiteral("timeText"), timeText},
            {QStringLiteral("color"), colorOf(e->calendarId)},
            {QStringLiteral("calendarName"), c ? c->name : QString()},
        });
    }
    return out;
}
