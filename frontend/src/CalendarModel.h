#pragma once

#include <QDate>
#include <QDateTime>
#include <QObject>
#include <QSet>
#include <QString>
#include <QTimer>
#include <QVariantList>
#include <QtQml/qqmlregistration.h>

#include <vector>

// Month view state: the displayed month, the selected day and the event
// occurrences of the visible 6-week grid (Monday first). Recurring events
// arrive already expanded from the daemon. QML asks eventsOn(day); `revision`
// changes whenever the answer may have changed.
class CalendarModel : public QObject
{
    Q_OBJECT
    QML_ELEMENT
    Q_PROPERTY(QDate month READ month NOTIFY monthChanged FINAL)
    Q_PROPERTY(QDate selectedDate READ selectedDate WRITE select NOTIFY selectedDateChanged FINAL)
    Q_PROPERTY(QDate gridStart READ gridStart NOTIFY monthChanged FINAL)
    Q_PROPERTY(QString monthTitle READ monthTitle NOTIFY monthChanged FINAL)
    Q_PROPERTY(QVariantList calendars READ calendars NOTIFY calendarsChanged FINAL)
    Q_PROPERTY(int revision READ revision NOTIFY revisionChanged FINAL)
    Q_PROPERTY(bool loading READ loading NOTIFY loadingChanged FINAL)
    Q_PROPERTY(int eventCount READ eventCount NOTIFY revisionChanged FINAL)

public:
    explicit CalendarModel(QObject *parent = nullptr);

    QDate month() const { return m_month; }
    QDate selectedDate() const { return m_selected; }
    QDate gridStart() const;
    QString monthTitle() const;
    QVariantList calendars() const;
    int revision() const { return m_revision; }
    bool loading() const { return m_pending > 0; }
    int eventCount() const { return int(m_events.size()); }

    // Occurrences overlapping `day` (all-day first, then by start), as maps with
    // id, summary, location, description, organizer, status, start, end
    // (QDateTime), allDay, recurring, multiDay, color, calendarName, timeText.
    // `revision` is unused: passing it makes QML bindings re-run when events change.
    Q_INVOKABLE QVariantList eventsOn(const QDate &day, int revision = 0) const;
    Q_INVOKABLE QDate gridDate(int index, const QDate &month = {}) const
    {
        Q_UNUSED(month); // dependency for QML, like `revision`
        return gridStart().addDays(index);
    }

    Q_INVOKABLE void select(const QDate &day);
    Q_INVOKABLE void moveDays(int n) { select(m_selected.addDays(n)); }
    Q_INVOKABLE void moveMonths(int n) { select(m_selected.addMonths(n)); }
    Q_INVOKABLE void goToday() { select(QDate::currentDate()); }
    Q_INVOKABLE void toggleCalendar(const QString &calendarId);
    Q_INVOKABLE void refresh();

signals:
    void monthChanged();
    void selectedDateChanged();
    void calendarsChanged();
    void revisionChanged();
    void loadingChanged();

private:
    struct Cal {
        QString id, accountId, name, color;
        bool readOnly = false;
    };
    struct Ev {
        QString id, calendarId, summary, location, description, organizer, status;
        QDateTime start, end;
        bool allDay = false, recurring = false;
    };

    void loadCalendars();
    void loadEvents();
    void setPending(int delta);
    QString colorOf(const QString &calendarId) const;
    const Cal *calendar(const QString &id) const;

    QDate m_month;
    QDate m_selected;
    std::vector<Cal> m_cals;
    std::vector<Ev> m_events;
    QSet<QString> m_hidden;
    int m_revision = 0;
    int m_pending = 0;
    int m_gen = 0;
    QTimer m_reload;
};
