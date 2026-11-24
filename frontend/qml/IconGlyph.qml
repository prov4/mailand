import QtQuick

// Tiny built-in icon set using plain Unicode glyphs (no icon packs).
Text {
    id: root
    property string name: "folder"

    readonly property var glyphs: ({
        "inbox": "✉", "star": "★", "sent": "➤", "drafts": "✎",
        "archive": "▣", "spam": "⚠", "trash": "⌫", "label": "#",
        "folder": "▸", "mail": "✉", "calendar": "▦", "contacts": "☺",
        "clip": "⧉"
    })

    text: glyphs[name] ?? glyphs["folder"]
    font.pixelSize: Theme.fontBody
    color: Theme.textMuted
    horizontalAlignment: Text.AlignHCenter
    verticalAlignment: Text.AlignVCenter
    Accessible.ignored: true
}
