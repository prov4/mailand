import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

Popup {
    id: root
    property string mode: "compose"   // "compose" | "reply"

    modal: true
    focus: true
    closePolicy: Popup.CloseOnEscape | Popup.CloseOnPressOutside
    anchors.centerIn: Overlay.overlay
    width: Math.min(560, Overlay.overlay ? Overlay.overlay.width - 48 : 560)
    padding: Theme.s5

    background: Rectangle {
        color: Theme.surface
        radius: Theme.radiusLarge
        border.color: Theme.border
    }
    Overlay.modal: Rectangle { color: "#99000000" }

    contentItem: ColumnLayout {
        spacing: Theme.s3
        Label {
            text: root.mode === "reply" ? "Reply" : "New message"
            color: Theme.text
            font.pixelSize: Theme.fontTitle
            font.bold: true
        }
        Label {
            text: "Composer arrives in Milestone 4"
            color: Theme.textMuted
            font.pixelSize: Theme.fontBody
            Layout.fillWidth: true
            wrapMode: Text.Wrap
        }
        Label {
            text: "Esc to close"
            color: Theme.textFaint
            font.pixelSize: Theme.fontSmall
        }
    }
}
