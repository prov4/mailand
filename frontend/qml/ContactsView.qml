pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Mailand

// Searchable contact list and the selected contact's details.
Rectangle {
    id: root
    required property ContactsModel contacts
    readonly property bool searchActive: search.activeFocus
    property var current: ({})
    color: Theme.bg

    function updateCurrent() { current = list.currentIndex >= 0 ? contacts.get(list.currentIndex) : ({}) }
    Connections {
        target: root.contacts
        function onModelReset() { root.updateCurrent() }
    }

    signal composeTo(string accountId, string address)

    function next() { if (list.count > 0) list.currentIndex = Math.min(list.currentIndex + 1, list.count - 1) }
    function previous() { if (list.count > 0) list.currentIndex = Math.max(list.currentIndex - 1, 0) }
    function focusSearch() { search.forceActiveFocus(); search.selectAll() }
    function composeToCurrent() {
        if (root.current.address)
            root.composeTo(root.current.accountId, root.current.address)
    }

    RowLayout {
        anchors.fill: parent
        spacing: 0

        // ---- List --------------------------------------------------------------------------
        ColumnLayout {
            Layout.preferredWidth: Theme.listWidth
            Layout.maximumWidth: Theme.listWidth
            Layout.fillWidth: false // a child's fillWidth would otherwise make the list take all space
            Layout.fillHeight: true
            spacing: 0

            TextField {
                id: search
                Layout.fillWidth: true
                Layout.margins: Theme.s2
                placeholderText: "Search contacts  (/)"
                color: Theme.text
                placeholderTextColor: Theme.textFaint
                selectionColor: Theme.selection
                font.pixelSize: Theme.fontBody
                leftPadding: Theme.s3
                topPadding: Theme.s2
                bottomPadding: Theme.s2
                background: Rectangle {
                    radius: Theme.radius
                    color: Theme.surface
                    border.color: search.activeFocus ? Theme.accent : Theme.border
                }
                onTextChanged: { root.contacts.query = text; list.currentIndex = 0 }
                Keys.onDownPressed: { root.next(); list.forceActiveFocus() }
                Keys.onEscapePressed: { if (text.length > 0) text = ""; else list.forceActiveFocus() }
                Keys.onReturnPressed: list.forceActiveFocus()
            }

            ListView {
                id: list
                Layout.fillWidth: true
                Layout.fillHeight: true
                clip: true
                model: root.contacts
                currentIndex: 0
                onCurrentIndexChanged: root.updateCurrent()
                boundsBehavior: Flickable.StopAtBounds
                highlightMoveDuration: 0
                keyNavigationEnabled: false
                ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

                delegate: Rectangle {
                    id: row
                    required property int index
                    required property string fullName
                    required property string email
                    required property string organization
                    required property string initials
                    required property string avatarColor
                    width: ListView.view.width
                    height: 56
                    color: ListView.isCurrentItem ? Theme.selection : hover.hovered ? Theme.surface : "transparent"

                    RowLayout {
                        anchors.fill: parent
                        anchors.leftMargin: Theme.s3
                        anchors.rightMargin: Theme.s3
                        spacing: Theme.s3
                        Rectangle {
                            Layout.preferredWidth: 34; Layout.preferredHeight: 34; radius: 17
                            color: row.avatarColor
                            Label { anchors.centerIn: parent; text: row.initials; color: "white"; font.pixelSize: 13; font.bold: true }
                        }
                        Column {
                            Layout.fillWidth: true
                            Label { width: parent.width; text: row.fullName; color: Theme.text; font.pixelSize: Theme.fontBody; elide: Text.ElideRight }
                            Label {
                                width: parent.width
                                text: row.email.length > 0 ? row.email : row.organization
                                color: Theme.textMuted
                                font.pixelSize: Theme.fontSmall
                                elide: Text.ElideRight
                            }
                        }
                    }
                    Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: 1; color: Theme.border; opacity: 0.5 }
                    HoverHandler { id: hover }
                    TapHandler {
                        onTapped: list.currentIndex = row.index
                        onDoubleTapped: { list.currentIndex = row.index; root.composeToCurrent() }
                    }
                }

                Label {
                    anchors.centerIn: parent
                    visible: list.count === 0 && !root.contacts.loading
                    text: search.text.length > 0 ? "No contacts match" : "No contacts synced yet.\nSee the README for setup."
                    horizontalAlignment: Text.AlignHCenter
                    color: Theme.textFaint
                    font.pixelSize: Theme.fontBody
                }
            }
        }

        Rectangle { Layout.fillHeight: true; Layout.preferredWidth: 1; color: Theme.border }

        // ---- Details -----------------------------------------------------------------------
        Item {
            Layout.fillWidth: true
            Layout.fillHeight: true

            ColumnLayout {
                visible: root.current.fullName !== undefined
                anchors { left: parent.left; right: parent.right; top: parent.top; margins: Theme.s5 }
                spacing: Theme.s3

                RowLayout {
                    spacing: Theme.s4
                    Rectangle {
                        Layout.preferredWidth: 64; Layout.preferredHeight: 64; radius: 32
                        color: root.current.avatarColor ?? Theme.surfaceAlt
                        Label { anchors.centerIn: parent; text: root.current.initials ?? ""; color: "white"; font.pixelSize: 24; font.bold: true }
                    }
                    Column {
                        Label { text: root.current.fullName ?? ""; color: Theme.text; font.pixelSize: Theme.fontTitle + 4; font.bold: true }
                        Label {
                            visible: text.length > 0
                            text: root.current.organization ?? ""
                            color: Theme.textMuted
                            font.pixelSize: Theme.fontBody
                        }
                    }
                }

                Label {
                    Layout.topMargin: Theme.s3
                    visible: (root.current.emails ?? []).length > 0
                    text: "Email"
                    color: Theme.textFaint
                    font.pixelSize: Theme.fontSmall
                }
                Repeater {
                    model: root.current.emails ?? []
                    delegate: Label {
                        id: mail
                        required property string modelData
                        text: mail.modelData
                        color: mailHover.hovered ? Theme.accent : Theme.text
                        font.pixelSize: Theme.fontBody
                        font.underline: mailHover.hovered
                        HoverHandler { id: mailHover; cursorShape: Qt.PointingHandCursor }
                        TapHandler {
                            onTapped: root.composeTo(root.current.accountId,
                                                     (root.current.fullName && root.current.fullName !== mail.modelData)
                                                     ? root.current.fullName + " <" + mail.modelData + ">" : mail.modelData)
                        }
                    }
                }

                Label {
                    Layout.topMargin: Theme.s2
                    visible: (root.current.phones ?? []).length > 0
                    text: "Phone"
                    color: Theme.textFaint
                    font.pixelSize: Theme.fontSmall
                }
                Repeater {
                    model: root.current.phones ?? []
                    delegate: TextEdit {
                        required property string modelData
                        text: modelData
                        readOnly: true
                        selectByMouse: true
                        color: Theme.text
                        selectionColor: Theme.selection
                        font.pixelSize: Theme.fontBody
                    }
                }

                Button {
                    id: write
                    Layout.topMargin: Theme.s3
                    visible: (root.current.address ?? "").length > 0
                    text: "Write email  (c)"
                    focusPolicy: Qt.NoFocus
                    leftPadding: Theme.s4
                    rightPadding: Theme.s4
                    topPadding: Theme.s2
                    bottomPadding: Theme.s2
                    onClicked: root.composeToCurrent()
                    background: Rectangle { radius: Theme.radius; color: write.down ? Qt.darker(Theme.accent, 1.2) : Theme.accent }
                    contentItem: Label {
                        text: write.text
                        color: Theme.accentText
                        font.pixelSize: Theme.fontBody
                        font.bold: true
                        horizontalAlignment: Text.AlignHCenter
                        verticalAlignment: Text.AlignVCenter
                    }
                }
            }
        }
    }
}
