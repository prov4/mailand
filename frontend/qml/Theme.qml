pragma Singleton
import QtQuick
import Mailand

// Design tokens for mailand. Follows the active Omarchy theme live via OmarchyTheme
// (with calm dark defaults when Omarchy is absent).
QtObject {
    // Surfaces
    readonly property color bg: OmarchyTheme.bg
    readonly property color surface: OmarchyTheme.surface
    readonly property color surfaceAlt: OmarchyTheme.surfaceAlt
    readonly property color border: OmarchyTheme.border
    // Text
    readonly property color text: OmarchyTheme.text
    readonly property color textMuted: OmarchyTheme.textMuted
    readonly property color textFaint: OmarchyTheme.textFaint
    // Accents
    readonly property color accent: OmarchyTheme.accent
    readonly property color accentText: OmarchyTheme.accentText
    readonly property color unreadDot: OmarchyTheme.unreadDot
    readonly property color flag: OmarchyTheme.flag
    readonly property color danger: OmarchyTheme.danger
    readonly property color success: OmarchyTheme.success
    readonly property color selection: OmarchyTheme.selection
    readonly property color selectionInactive: OmarchyTheme.selectionInactive
    // Light canvas for HTML mail
    readonly property color mailCanvas: "#ffffff"

    // Spacing scale
    readonly property int s1: 4
    readonly property int s2: 8
    readonly property int s3: 12
    readonly property int s4: 16
    readonly property int s5: 24
    readonly property int radius: 6
    readonly property int radiusLarge: 10

    // Typography
    readonly property string fontFamily: Application.font.family
    readonly property string monoFamily: "monospace"
    readonly property int fontSmall: 12
    readonly property int fontBody: 14
    readonly property int fontTitle: 18

    // Layout
    readonly property int sidebarWidth: 220
    readonly property int listWidth: 380
    readonly property int rowHeight: 68
    readonly property int statusHeight: 24
}
