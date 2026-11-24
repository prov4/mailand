pragma Singleton
import QtQuick

// Design tokens for mailand. Calm dark palette; contrast ratios of text on bg/surface are >= 7:1,
// textMuted >= 4.5:1.
QtObject {
    // Surfaces
    readonly property color bg: "#14161a"
    readonly property color surface: "#1a1d23"
    readonly property color surfaceAlt: "#21252d"
    readonly property color border: "#2b303a"
    // Text
    readonly property color text: "#e6e9ef"
    readonly property color textMuted: "#a0a8b6"
    readonly property color textFaint: "#7d8595"
    // Accents
    readonly property color accent: "#7aa2f7"
    readonly property color accentText: "#0f1115"
    readonly property color unreadDot: "#7aa2f7"
    readonly property color flag: "#e5c07b"
    readonly property color danger: "#f07178"
    readonly property color success: "#8fd19e"
    readonly property color selection: "#2a3550"
    readonly property color selectionInactive: "#232a3a"
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
