import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

Popup {
    id: root
    modal: true
    focus: true
    closePolicy: Popup.CloseOnEscape | Popup.CloseOnPressOutside
    anchors.centerIn: Overlay.overlay
    padding: Theme.s5

    readonly property var bindings: [
        { keys: "j / k", what: "Next / previous message (opens it)" },
        { keys: "Enter / l", what: "Focus reader pane" },
        { keys: "h / Esc", what: "Back to message list" },
        { keys: "/", what: "Search" },
        { keys: "g g / G", what: "First / last message" },
        { keys: "J / K", what: "Next / previous folder" },
        { keys: "c", what: "Compose" },
        { keys: "r / R", what: "Reply / reply all" },
        { keys: "f", what: "Forward" },
        { keys: "Ctrl+Enter", what: "Send (in composer)" },
        { keys: "m", what: "Toggle read / unread" },
        { keys: "s", what: "Toggle star" },
        { keys: "Ctrl+R / F5", what: "Sync current account" },
        { keys: "?", what: "This help" },
        { keys: "Ctrl+Q", what: "Quit" }
    ]

    background: Rectangle {
        color: Theme.surface
        radius: Theme.radiusLarge
        border.color: Theme.border
    }
    Overlay.modal: Rectangle { color: "#99000000" }

    contentItem: ColumnLayout {
        spacing: Theme.s2
        Label {
            text: "Keyboard shortcuts"
            color: Theme.text
            font.pixelSize: Theme.fontTitle
            font.bold: true
            Layout.bottomMargin: Theme.s2
        }
        Repeater {
            model: root.bindings
            delegate: RowLayout {
                id: row
                required property var modelData
                spacing: Theme.s4
                Label {
                    text: row.modelData.keys
                    color: Theme.accent
                    font.family: Theme.monoFamily
                    font.pixelSize: Theme.fontBody
                    Layout.preferredWidth: 110
                }
                Label {
                    text: row.modelData.what
                    color: Theme.text
                    font.pixelSize: Theme.fontBody
                }
            }
        }
        Label {
            text: "In the reader pane, j / k still move between messages."
            color: Theme.textFaint
            font.pixelSize: Theme.fontSmall
            Layout.topMargin: Theme.s2
        }
    }
}
