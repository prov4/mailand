import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Mailand

ApplicationWindow {
    id: root
    visible: true
    width: 720
    height: 480
    title: "mailand"
    flags: Qt.FramelessWindowHint | Qt.Window
    color: "#16181d"

    RpcClient {
        id: rpc
    }

    // F5 / Ctrl+R refresh ("r" is reserved for reply).
    Shortcut { sequences: ["F5", "Ctrl+R"]; onActivated: rpc.refreshAccounts() }
    Shortcut { sequences: ["Ctrl+Q"]; onActivated: Qt.quit() }

    Component.onCompleted: rpc.refreshAccounts()

    ColumnLayout {
        anchors.fill: parent
        anchors.margins: 16
        spacing: 12

        RowLayout {
            spacing: 8
            Rectangle {
                Layout.preferredWidth: 10; Layout.preferredHeight: 10; radius: 5
                color: rpc.connected ? "#7fd18b" : "#e06c75"
            }
            Label {
                text: rpc.connected ? "Connected to daemon" : "Not connected"
                color: "#c8ccd4"
            }
        }

        Label {
            visible: rpc.lastError.length > 0
            text: rpc.lastError
            color: "#e06c75"
            wrapMode: Text.Wrap
            Layout.fillWidth: true
        }

        ListView {
            Layout.fillWidth: true
            Layout.fillHeight: true
            clip: true
            model: rpc.accounts
            spacing: 4
            delegate: Rectangle {
                id: row
                required property var modelData
                width: ListView.view.width
                height: 52
                radius: 6
                color: "#1f232b"
                Column {
                    anchors.verticalCenter: parent.verticalCenter
                    x: 12
                    Label { text: row.modelData.name; color: "#e6e9ef"; font.bold: true }
                    Label { text: row.modelData.email; color: "#8b92a0" }
                }
            }
        }

        Label {
            text: "F5 / Ctrl+R refresh"
            color: "#5c6370"
            font.pixelSize: 11
        }
    }
}
