pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Mailand

Rectangle {
    id: root

    required property FolderModel folders
    property CalendarModel calendar
    property string accountId
    property string folderId
    property string view: "mail" // "mail" | "calendar" | "contacts"
    property alias folderList: list

    signal accountSelected(string accountId)
    signal folderSelected(string folderId)
    signal viewSelected(string view)

    color: Theme.surface

    ColumnLayout {
        anchors.fill: parent
        anchors.margins: Theme.s2
        spacing: Theme.s2

        // Send/Receive: sending is immediate (no outbox), so this checks every
        // account for new mail right away instead of waiting for IDLE/polling.
        Button {
            id: syncButton
            Layout.fillWidth: true
            implicitHeight: 32
            enabled: Rpc.connected
            hoverEnabled: true
            focusPolicy: Qt.NoFocus
            text: (Rpc.syncing ? "⟳  Receiving…" : "⟳  Send/Receive")
            ToolTip.visible: hovered
            ToolTip.delay: 400
            ToolTip.text: "Check all accounts for new mail, events and contacts (Ctrl+R / F5)"
            onClicked: Rpc.triggerSync("")
            background: Rectangle {
                radius: Theme.radius
                color: syncButton.down ? Theme.selection
                     : syncButton.hovered ? Theme.surfaceAlt : Theme.bg
                border.color: Theme.border
            }
            contentItem: Label {
                text: syncButton.text
                color: syncButton.enabled ? Theme.text : Theme.textFaint
                font.pixelSize: Theme.fontSmall
                horizontalAlignment: Text.AlignHCenter
                verticalAlignment: Text.AlignVCenter
            }
        }

        // Account switcher (only with more than one account)
        Repeater {
            model: Rpc.accounts.length > 1 && root.view === "mail" ? Rpc.accounts : []
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
            visible: Rpc.accounts.length > 1 && root.view === "mail"
            Layout.fillWidth: true
            Layout.preferredHeight: 1
            color: Theme.border
        }

        // Calendar list (calendar view): click to show/hide
        ListView {
            id: calList
            visible: root.view === "calendar"
            Layout.fillWidth: true
            Layout.fillHeight: true
            clip: true
            model: root.calendar ? root.calendar.calendars : []
            boundsBehavior: Flickable.StopAtBounds
            delegate: Item {
                id: calItem
                required property var modelData
                width: ListView.view.width
                height: 32
                Rectangle {
                    anchors.fill: parent
                    radius: Theme.radius
                    color: calHover.hovered ? Theme.surfaceAlt : "transparent"
                }
                RowLayout {
                    anchors.fill: parent
                    anchors.leftMargin: Theme.s3
                    anchors.rightMargin: Theme.s3
                    spacing: Theme.s2
                    Rectangle {
                        Layout.preferredWidth: 14; Layout.preferredHeight: 14; radius: 3
                        color: calItem.modelData.visible ? calItem.modelData.color : "transparent"
                        border.color: calItem.modelData.color
                        border.width: 2
                    }
                    Text {
                        Layout.fillWidth: true
                        text: calItem.modelData.name
                        color: calItem.modelData.visible ? Theme.text : Theme.textFaint
                        font.pixelSize: Theme.fontBody
                        elide: Text.ElideRight
                    }
                    Text {
                        visible: Rpc.accounts.length > 1
                        Layout.maximumWidth: 80
                        text: calItem.modelData.accountName
                        color: Theme.textFaint
                        font.pixelSize: 11
                        elide: Text.ElideRight
                    }
                }
                HoverHandler { id: calHover }
                TapHandler { onTapped: root.calendar.toggleCalendar(calItem.modelData.id) }
            }
        }

        // Contacts view: nothing to pick here
        Item {
            visible: root.view === "contacts"
            Layout.fillWidth: true
            Layout.fillHeight: true
        }

        // Folder list
        ListView {
            id: list
            visible: root.view === "mail"
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
                    { name: "Mail", key: "mail", shortcut: "Ctrl+1" },
                    { name: "Calendar", key: "calendar", shortcut: "Ctrl+2" },
                    { name: "Contacts", key: "contacts", shortcut: "Ctrl+3" }
                ]
                delegate: ToolButton {
                    id: view
                    required property var modelData
                    readonly property bool current: root.view === view.modelData.key
                    Layout.fillWidth: true
                    text: view.modelData.name
                    hoverEnabled: true
                    focusPolicy: Qt.NoFocus
                    implicitHeight: 32
                    onClicked: root.viewSelected(view.modelData.key)
                    ToolTip.visible: hovered
                    ToolTip.text: view.modelData.name + " (" + view.modelData.shortcut + ")"
                    ToolTip.delay: 500
                    background: Rectangle {
                        radius: Theme.radius
                        color: view.current ? Theme.selection : view.hovered ? Theme.surfaceAlt : "transparent"
                    }
                    contentItem: Label {
                        text: view.text
                        color: view.current ? Theme.text : Theme.textMuted
                        font.pixelSize: Theme.fontSmall
                        horizontalAlignment: Text.AlignHCenter
                        verticalAlignment: Text.AlignVCenter
                    }
                }
            }
        }
    }
}
