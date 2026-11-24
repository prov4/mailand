pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Mailand

Rectangle {
    id: root

    required property FolderModel folders
    property string accountId
    property string folderId
    property alias folderList: list

    signal accountSelected(string accountId)
    signal folderSelected(string folderId)

    color: Theme.surface

    ColumnLayout {
        anchors.fill: parent
        anchors.margins: Theme.s2
        spacing: Theme.s2

        // Account switcher (only with more than one account)
        Repeater {
            model: Rpc.accounts.length > 1 ? Rpc.accounts : []
            delegate: Item {
                id: acct
                required property var modelData
                readonly property bool current: acct.modelData.id === root.accountId
                Layout.fillWidth: true
                implicitHeight: 44

                Rectangle {
                    anchors.fill: parent
                    radius: Theme.radius
                    color: acct.current ? Theme.surfaceAlt : hover.hovered ? Theme.bg : "transparent"
                    border.color: acct.current ? Theme.border : "transparent"
                }
                Column {
                    anchors { left: parent.left; right: parent.right; leftMargin: Theme.s3; rightMargin: Theme.s3; verticalCenter: parent.verticalCenter }
                    Text {
                        text: acct.modelData.name
                        color: acct.current ? Theme.text : Theme.textMuted
                        font.pixelSize: Theme.fontBody
                        font.bold: true
                        elide: Text.ElideRight
                        width: parent.width
                    }
                    Text {
                        text: acct.modelData.email
                        color: Theme.textMuted
                        font.pixelSize: Theme.fontSmall
                        elide: Text.ElideRight
                        width: parent.width
                    }
                }
                HoverHandler { id: hover }
                TapHandler { onTapped: root.accountSelected(acct.modelData.id) }
            }
        }
        Rectangle {
            visible: Rpc.accounts.length > 1
            Layout.fillWidth: true
            Layout.preferredHeight: 1
            color: Theme.border
        }

        // Folder list
        ListView {
            id: list
            Layout.fillWidth: true
            Layout.fillHeight: true
            clip: true
            model: root.folders
            boundsBehavior: Flickable.StopAtBounds
            activeFocusOnTab: false
            ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

            delegate: Item {
                id: item
                required property string folderId
                required property string displayName
                required property string icon
                required property int unreadCount
                readonly property bool current: item.folderId === root.folderId

                width: ListView.view.width
                height: 34

                Rectangle {
                    anchors.fill: parent
                    radius: Theme.radius
                    color: item.current ? Theme.selection
                         : hover.hovered ? Theme.surfaceAlt : "transparent"
                }
                RowLayout {
                    anchors.fill: parent
                    anchors.leftMargin: Theme.s3
                    anchors.rightMargin: Theme.s3
                    spacing: Theme.s2
                    IconGlyph {
                        name: item.icon
                        color: item.current ? Theme.accent : Theme.textMuted
                        Layout.preferredWidth: 20
                    }
                    Text {
                        text: item.displayName
                        color: item.current || item.unreadCount > 0 ? Theme.text : Theme.textMuted
                        font.pixelSize: Theme.fontBody
                        font.bold: item.unreadCount > 0
                        elide: Text.ElideRight
                        Layout.fillWidth: true
                    }
                    Badge { count: item.unreadCount }
                }
                HoverHandler { id: hover }
                TapHandler { onTapped: root.folderSelected(item.folderId) }
            }
        }

        Rectangle {
            Layout.fillWidth: true
            Layout.preferredHeight: 1
            color: Theme.border
        }

        // View switcher
        RowLayout {
            Layout.fillWidth: true
            spacing: Theme.s1
            Repeater {
                model: [
                    { name: "Mail", icon: "mail", available: true },
                    { name: "Calendar", icon: "calendar", available: false },
                    { name: "Contacts", icon: "contacts", available: false }
                ]
                delegate: ToolButton {
                    id: view
                    required property var modelData
                    Layout.fillWidth: true
                    text: view.modelData.name
                    hoverEnabled: true
                    implicitHeight: 32
                    ToolTip.visible: hovered && !view.modelData.available
                    ToolTip.text: "Milestone 5"
                    ToolTip.delay: 300
                    background: Rectangle {
                        radius: Theme.radius
                        color: view.modelData.available ? Theme.selection : "transparent"
                    }
                    contentItem: Label {
                        text: view.text
                        color: view.modelData.available ? Theme.text : Theme.textFaint
                        font.pixelSize: Theme.fontSmall
                        horizontalAlignment: Text.AlignHCenter
                        verticalAlignment: Text.AlignVCenter
                    }
                }
            }
        }
    }
}
