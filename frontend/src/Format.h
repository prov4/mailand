#pragma once

#include <QString>
#include <QtTypes>

namespace Format {

// Today "14:32"; this year "24 Nov"; older "24.11.2025" (local time).
QString shortDate(qint64 unixSeconds);
// "Mon, 24 Nov 2026 14:32"
QString longDate(qint64 unixSeconds);
// "1.2 MB"
QString sizeText(quint64 bytes);
// Search snippets: \x02 = match start, \x03 = match end.
QString snippetPlain(const QString &raw);
QString snippetRich(const QString &raw);

} // namespace Format
