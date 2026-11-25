#include "OmarchyTheme.h"

#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QHash>
#include <QRegularExpression>

#include <cmath>

namespace {

QColor blend(const QColor &a, const QColor &b, double weightA)
{
    const double wb = 1.0 - weightA;
    return QColor::fromRgbF(
        a.redF() * weightA + b.redF() * wb,
        a.greenF() * weightA + b.greenF() * wb,
        a.blueF() * weightA + b.blueF() * wb);
}

// WCAG relative luminance, used to choose a readable foreground on accent colors.
double luminance(const QColor &c)
{
    auto lin = [](double v) { return v <= 0.04045 ? v / 12.92 : std::pow((v + 0.055) / 1.055, 2.4); };
    return 0.2126 * lin(c.redF()) + 0.7152 * lin(c.greenF()) + 0.0722 * lin(c.blueF());
}

QHash<QString, QString> parseColorsToml(const QString &path)
{
    QHash<QString, QString> out;
    QFile f(path);
    if (!f.open(QIODevice::ReadOnly))
        return out;
    static const QRegularExpression re(
        QStringLiteral(R"(^\s*([a-zA-Z0-9_]+)\s*=\s*["']([^"']+)["'])"),
        QRegularExpression::MultilineOption);
    const QString text = QString::fromUtf8(f.readAll());
    auto it = re.globalMatch(text);
    while (it.hasNext()) {
        const auto m = it.next();
        out.insert(m.captured(1), m.captured(2));
    }
    return out;
}

QColor colorOr(const QHash<QString, QString> &map, const QString &key, const QColor &fallback)
{
    const QString v = map.value(key);
    if (!v.isEmpty()) {
        QColor c(v);
        if (c.isValid())
            return c;
    }
    return fallback;
}

} // namespace

OmarchyTheme::OmarchyTheme(QObject *parent)
    : QObject(parent),
      m_directory(QDir::homePath() + QStringLiteral("/.local/state/omarchy/current"))
{
    applyDefaults();

    m_debounce.setSingleShot(true);
    m_debounce.setInterval(80);
    connect(&m_watcher, &QFileSystemWatcher::directoryChanged, this, [this] { m_debounce.start(); });
    connect(&m_watcher, &QFileSystemWatcher::fileChanged, this, [this] { m_debounce.start(); });
    connect(&m_debounce, &QTimer::timeout, this, &OmarchyTheme::reload);

    reload();
}

void OmarchyTheme::applyDefaults()
{
    m_bg = QColor(QStringLiteral("#14161a"));
    m_surface = QColor(QStringLiteral("#1a1d23"));
    m_surfaceAlt = QColor(QStringLiteral("#21252d"));
    m_border = QColor(QStringLiteral("#2b303a"));
    m_text = QColor(QStringLiteral("#e6e9ef"));
    m_textMuted = QColor(QStringLiteral("#a0a8b6"));
    m_textFaint = QColor(QStringLiteral("#7d8595"));
    m_accent = QColor(QStringLiteral("#7aa2f7"));
    m_accentText = QColor(QStringLiteral("#0f1115"));
    m_unreadDot = QColor(QStringLiteral("#7aa2f7"));
    m_flag = QColor(QStringLiteral("#e5c07b"));
    m_danger = QColor(QStringLiteral("#f07178"));
    m_success = QColor(QStringLiteral("#8fd19e"));
    m_selection = QColor(QStringLiteral("#2a3550"));
    m_selectionInactive = QColor(QStringLiteral("#232a3a"));
}

void OmarchyTheme::reload()
{
    // Re-arm watchers on every change because Omarchy theme switches replace symlinks/files.
    const QStringList oldPaths = m_watcher.files() + m_watcher.directories();
    if (!oldPaths.isEmpty())
        m_watcher.removePaths(oldPaths);

    QString ancestor = m_directory;
    while (!QFileInfo::exists(ancestor) && ancestor != QLatin1String("/"))
        ancestor = QFileInfo(ancestor).absolutePath();
    QStringList candidates{
        ancestor,
        QFileInfo(ancestor).absolutePath(),
        m_directory,
        m_directory + QStringLiteral("/theme"),
        m_directory + QStringLiteral("/theme/colors.toml"),
    };
    candidates.removeDuplicates();
    for (const QString &p : candidates) {
        if (QFileInfo::exists(p))
            m_watcher.addPath(p);
    }

    const QHash<QString, QString> kv = parseColorsToml(m_directory + QStringLiteral("/theme/colors.toml"));
    if (kv.isEmpty()) {
        return;
    }

    const QColor prevBg = m_bg, prevSurface = m_surface, prevSurfaceAlt = m_surfaceAlt,
                 prevBorder = m_border, prevText = m_text, prevTextMuted = m_textMuted,
                 prevTextFaint = m_textFaint, prevAccent = m_accent, prevAccentText = m_accentText,
                 prevFlag = m_flag, prevDanger = m_danger, prevSuccess = m_success,
                 prevSelection = m_selection, prevSelectionInactive = m_selectionInactive;

    const QColor bg = colorOr(kv, QStringLiteral("background"), m_bg);
    const QColor fg = colorOr(kv, QStringLiteral("foreground"), m_text);
    const bool isLight = kv.value(QStringLiteral("mode")).compare(QStringLiteral("light"), Qt::CaseInsensitive) == 0
                         || luminance(bg) > 0.45;

    m_bg = bg;
    m_surface = colorOr(kv, isLight ? QStringLiteral("dark_background") : QStringLiteral("lighter_background"),
                        blend(bg, fg, 0.95));
    m_surfaceAlt = colorOr(kv, isLight ? QStringLiteral("lighter_background") : QStringLiteral("darker_background"),
                           blend(bg, fg, 0.90));
    if (m_surfaceAlt == m_surface)
        m_surfaceAlt = blend(m_surface, fg, 0.94);

    const QColor muted = colorOr(kv, QStringLiteral("muted"), blend(fg, bg, 0.45));
    m_border = blend(muted, bg, isLight ? 0.45 : 0.35);

    m_text = fg;
    m_textMuted = blend(fg, bg, 0.72);
    m_textFaint = blend(fg, bg, 0.52);

    m_accent = colorOr(kv, QStringLiteral("accent"), m_accent);
    m_accentText = luminance(m_accent) > 0.179 ? QColor(QStringLiteral("#0f1115")) : QColor(QStringLiteral("#ffffff"));
    m_unreadDot = m_accent;

    m_flag = colorOr(kv, QStringLiteral("yellow"), m_flag);
    m_danger = colorOr(kv, QStringLiteral("red"), m_danger);
    m_success = colorOr(kv, QStringLiteral("green"), m_success);

    m_selection = colorOr(kv, QStringLiteral("selection"), blend(m_accent, bg, 0.25));
    m_selectionInactive = blend(m_selection, bg, 0.60);

    if (m_bg != prevBg || m_surface != prevSurface || m_surfaceAlt != prevSurfaceAlt
        || m_border != prevBorder || m_text != prevText || m_textMuted != prevTextMuted
        || m_textFaint != prevTextFaint || m_accent != prevAccent || m_accentText != prevAccentText
        || m_flag != prevFlag || m_danger != prevDanger || m_success != prevSuccess
        || m_selection != prevSelection || m_selectionInactive != prevSelectionInactive) {
        emit changed();
    }
}
