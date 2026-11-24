#include "Format.h"

#include <QDate>
#include <QDateTime>
#include <QLocale>
#include <QTimeZone>

namespace Format {

QString shortDate(qint64 unixSeconds)
{
    const QDateTime dt = QDateTime::fromSecsSinceEpoch(unixSeconds, QTimeZone::LocalTime);
    const QDate today = QDate::currentDate();
    const QLocale loc;
    if (dt.date() == today)
        return loc.toString(dt.time(), QStringLiteral("HH:mm"));
    if (dt.date().year() == today.year())
        return loc.toString(dt.date(), QStringLiteral("d MMM"));
    return loc.toString(dt.date(), QStringLiteral("dd.MM.yyyy"));
}

QString longDate(qint64 unixSeconds)
{
    const QDateTime dt = QDateTime::fromSecsSinceEpoch(unixSeconds, QTimeZone::LocalTime);
    return QLocale().toString(dt, QStringLiteral("ddd, d MMM yyyy HH:mm"));
}

QString sizeText(quint64 bytes)
{
    return QLocale().formattedDataSize(static_cast<qint64>(bytes), 1, QLocale::DataSizeTraditionalFormat);
}

QString snippetPlain(const QString &raw)
{
    QString out;
    out.reserve(raw.size());
    for (const QChar c : raw)
        if (c != QChar(0x02) && c != QChar(0x03))
            out += c;
    return out;
}

QString snippetRich(const QString &raw)
{
    QString out, run;
    auto flush = [&] {
        out += run.toHtmlEscaped();
        run.clear();
    };
    bool open = false;
    for (const QChar c : raw) {
        if (c == QChar(0x02)) {
            flush();
            if (!open) { out += QStringLiteral("<b>"); open = true; }
        } else if (c == QChar(0x03)) {
            flush();
            if (open) { out += QStringLiteral("</b>"); open = false; }
        } else {
            run += c;
        }
    }
    flush();
    if (open)
        out += QStringLiteral("</b>");
    return out;
}

} // namespace Format
