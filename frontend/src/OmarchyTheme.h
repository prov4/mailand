#pragma once

#include <QColor>
#include <QFileSystemWatcher>
#include <QObject>
#include <QString>
#include <QTimer>
#include <QtQml/qqmlregistration.h>

// Watches Omarchy's active theme (~/.local/state/omarchy/current/theme/colors.toml)
// and exposes semantic UI colors to Theme.qml so the window updates live whenever
// the user switches Omarchy themes (both dark and light). Falls back to mailand's
// default dark palette when Omarchy is not present.
class OmarchyTheme : public QObject
{
    Q_OBJECT
    QML_ELEMENT
    QML_SINGLETON

    Q_PROPERTY(QColor bg READ bg NOTIFY changed FINAL)
    Q_PROPERTY(QColor surface READ surface NOTIFY changed FINAL)
    Q_PROPERTY(QColor surfaceAlt READ surfaceAlt NOTIFY changed FINAL)
    Q_PROPERTY(QColor border READ border NOTIFY changed FINAL)
    Q_PROPERTY(QColor text READ text NOTIFY changed FINAL)
    Q_PROPERTY(QColor textMuted READ textMuted NOTIFY changed FINAL)
    Q_PROPERTY(QColor textFaint READ textFaint NOTIFY changed FINAL)
    Q_PROPERTY(QColor accent READ accent NOTIFY changed FINAL)
    Q_PROPERTY(QColor accentText READ accentText NOTIFY changed FINAL)
    Q_PROPERTY(QColor unreadDot READ unreadDot NOTIFY changed FINAL)
    Q_PROPERTY(QColor flag READ flag NOTIFY changed FINAL)
    Q_PROPERTY(QColor danger READ danger NOTIFY changed FINAL)
    Q_PROPERTY(QColor success READ success NOTIFY changed FINAL)
    Q_PROPERTY(QColor selection READ selection NOTIFY changed FINAL)
    Q_PROPERTY(QColor selectionInactive READ selectionInactive NOTIFY changed FINAL)

public:
    explicit OmarchyTheme(QObject *parent = nullptr);

    QColor bg() const { return m_bg; }
    QColor surface() const { return m_surface; }
    QColor surfaceAlt() const { return m_surfaceAlt; }
    QColor border() const { return m_border; }
    QColor text() const { return m_text; }
    QColor textMuted() const { return m_textMuted; }
    QColor textFaint() const { return m_textFaint; }
    QColor accent() const { return m_accent; }
    QColor accentText() const { return m_accentText; }
    QColor unreadDot() const { return m_unreadDot; }
    QColor flag() const { return m_flag; }
    QColor danger() const { return m_danger; }
    QColor success() const { return m_success; }
    QColor selection() const { return m_selection; }
    QColor selectionInactive() const { return m_selectionInactive; }

signals:
    void changed();

private:
    void reload();
    void applyDefaults();

    QString m_directory;
    QFileSystemWatcher m_watcher;
    QTimer m_debounce;

    QColor m_bg;
    QColor m_surface;
    QColor m_surfaceAlt;
    QColor m_border;
    QColor m_text;
    QColor m_textMuted;
    QColor m_textFaint;
    QColor m_accent;
    QColor m_accentText;
    QColor m_unreadDot;
    QColor m_flag;
    QColor m_danger;
    QColor m_success;
    QColor m_selection;
    QColor m_selectionInactive;
};
