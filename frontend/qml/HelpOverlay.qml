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

    readonly property var groups: [
        { title: "Everywhere", rows: [
            { keys: "Ctrl+1 / 2 / 3", what: "Mail / Calendar / Contacts" },
            { keys: "c", what: "Compose" },
            { keys: "Ctrl+R / F5", what: "Send/Receive (mail, calendars, contacts)" },
            { keys: "?", what: "This help" },
            { keys: "Ctrl+Q", what: "Quit" }
        ] },
        { title: "Mail", rows: [
            { keys: "j / k", what: "Next / previous message" },
            { keys: "Enter / l", what: "Focus reader pane" },
            { keys: "h / Esc", what: "Back to message list" },
            { keys: "/", what: "Search" },
            { keys: "g g / G", what: "First / last message" },
            { keys: "J / K", what: "Next / previous folder" },
            { keys: "r / R / f", what: "Reply / reply all / forward" },
            { keys: "m / s", what: "Toggle read / star" }
        ] },
        { title: "Composer", rows: [
            { keys: "Ctrl+Enter", what: "Send" },
            { keys: "Ctrl+Shift+A", what: "Attach files" },
            { keys: "↑ ↓ Enter", what: "Pick an address suggestion" },
            { keys: "Esc", what: "Close" }
        ] },
        { title: "Calendar", rows: [
            { keys: "h / l", what: "Previous / next day" },
            { keys: "k / j", what: "Previous / next week" },
            { keys: "p / n", what: "Previous / next month" },
            { keys: "t", what: "Today" },
            { keys: "Enter / click", what: "Event details, invitations" },
            { keys: "y / m / n", what: "Answer invitation: yes / maybe / no" }
        ] },
        { title: "Contacts", rows: [
            { keys: "j / k", what: "Next / previous contact" },
            { keys: "/", what: "Search" },
            { keys: "c / Enter", what: "Write to contact" },
            { keys: "n / e", what: "New contact / edit contact" }
        ] }
    ]

    background: Rectangle {
        color: Theme.surface
        radius: Theme.radiusLarge
        border.color: Theme.border
    }
    Overlay.modal: Rectangle { color: "#99000000" }

    contentItem: ColumnLayout {
        spacing: Theme.s2
        RowLayout {
            Layout.bottomMargin: Theme.s2
            Label {
                text: "Keyboard shortcuts"
                color: Theme.text
                font.pixelSize: Theme.fontTitle
                font.bold: true
                Layout.fillWidth: true
            }
            Label {
                text: "mailand " + Qt.application.version
                color: Theme.textFaint
                font.pixelSize: Theme.fontSmall
            }
        }
        GridLayout {
            columns: 2
            columnSpacing: Theme.s5 * 2
            rowSpacing: Theme.s3
            Repeater {
                model: root.groups
                delegate: ColumnLayout {
                    id: group
                    required property var modelData
                    Layout.alignment: Qt.AlignTop
                    spacing: 4
                    Label {
                        text: group.modelData.title
                        color: Theme.textMuted
                        font.pixelSize: Theme.fontSmall
                        font.bold: true
                        Layout.bottomMargin: 2
                    }
                    Repeater {
                        model: group.modelData.rows
                        delegate: RowLayout {
                            id: row
                            required property var modelData
                            spacing: Theme.s3
                            Label {
                                text: row.modelData.keys
                                color: Theme.accent
                                font.family: Theme.monoFamily
                                font.pixelSize: Theme.fontSmall + 1
                                Layout.preferredWidth: 120
                            }
                            Label {
                                text: row.modelData.what
                                color: Theme.text
                                font.pixelSize: Theme.fontSmall + 1
                            }
                        }
                    }
                }
            }
        }
    }
}
