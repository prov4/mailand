pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Mailand

FocusScope {
    id: root

    required property MessageModel messages
    property string accountId
    // Selection is tracked by message id so it survives incremental model updates.
    property string currentMessageId
    readonly property bool searchActive: searchField.activeFocus
    readonly property bool searching: root.messages.searchQuery.length > 0

    signal messageActivated(string messageId)
    signal readerRequested()

    function currentRow(): int {
        return root.currentMessageId.length > 0 ? root.messages.indexOfMessage(root.currentMessageId) : -1
    }

    // Selects a row and opens it. Returns false if the row does not exist.
    function selectRow(row: int): bool {
        if (row < 0 || row >= root.messages.count)
            return false
        const m = root.messages.get(row)
        list.currentIndex = row
        list.positionViewAtIndex(row, ListView.Contain)
        root.currentMessageId = m.messageId
        root.messageActivated(m.messageId)
        return true
    }
    function next() { selectRow(Math.min(currentRow() + 1, root.messages.count - 1)) }
    function previous() { selectRow(Math.max(currentRow() - 1, 0)) }
    function first() { selectRow(0) }
    function last() {
        // Pages load lazily; jump to the end of what is loaded.
        selectRow(root.messages.count - 1)
    }
    function openFirst() { first() }
    function clearSelection() { currentMessageId = "" }
    function focusSearch() { searchField.forceActiveFocus(); searchField.selectAll() }
    function focusList() { list.forceActiveFocus() }
    function clearSearch() { searchField.text = "" }

    // Keep ListView.currentIndex in step with the selected id when rows are inserted/removed.
    Connections {
        target: root.messages
        function onRowsInserted() { root.resync() }
        function onRowsRemoved() { root.resync() }
        function onModelReset() { root.resync() }
    }
    function resync() {
        const r = currentRow()
        if (r >= 0)
            list.currentIndex = r
    }

    Rectangle { anchors.fill: parent; color: Theme.bg }
    Rectangle {
        anchors.right: parent.right
        width: 1; height: parent.height
        color: Theme.border
    }

    ColumnLayout {
        anchors.fill: parent
        anchors.rightMargin: 1
        spacing: 0

        // Search + Unread filter toggle
        RowLayout {
            Layout.fillWidth: true
            Layout.margins: Theme.s2
            spacing: Theme.s2

            TextField {
                id: searchField
                Layout.fillWidth: true
                implicitHeight: 36
                placeholderText: "Search mail  (/)"
                placeholderTextColor: Theme.textFaint
                color: Theme.text
                selectByMouse: true
                font.pixelSize: Theme.fontBody
                leftPadding: Theme.s3
                background: Rectangle {
                    radius: Theme.radius
                    color: Theme.surface
                    border.color: searchField.activeFocus ? Theme.accent : Theme.border
                }
                onTextChanged: root.messages.searchQuery = text
                Keys.onEscapePressed: {
                    text = ""
                    list.forceActiveFocus()
                }
                Keys.onReturnPressed: list.forceActiveFocus()
                Keys.onDownPressed: { list.forceActiveFocus(); root.selectRow(0) }
            }

            Button {
                id: unreadBtn
                text: "Unread"
                checkable: true
                checked: root.messages.unreadOnly
                focusPolicy: Qt.NoFocus
                implicitHeight: 36
                onClicked: root.messages.unreadOnly = !root.messages.unreadOnly
                ToolTip.visible: hovered
                ToolTip.delay: 500
                ToolTip.text: "Toggle unread only (u)"
                contentItem: Label {
                    text: unreadBtn.text
                    color: unreadBtn.checked ? Theme.accentText : Theme.textMuted
                    font.pixelSize: Theme.fontSmall
                    font.bold: unreadBtn.checked
                    horizontalAlignment: Text.AlignHCenter
                    verticalAlignment: Text.AlignVCenter
                    leftPadding: Theme.s2
                    rightPadding: Theme.s2
                }
                background: Rectangle {
                    radius: Theme.radius
                    color: unreadBtn.checked ? Theme.accent : Theme.surface
                    border.color: unreadBtn.checked ? Theme.accent : Theme.border
                }
            }
        }
        Rectangle { Layout.fillWidth: true; Layout.preferredHeight: 1; color: Theme.border }

        // Loading bar
        ProgressBar {
            Layout.fillWidth: true
            Layout.preferredHeight: 2
            visible: root.messages.loading
            indeterminate: true
            background: Item {}
            padding: 0
        }

        ListView {
            id: list
            Layout.fillWidth: true
            Layout.fillHeight: true
            clip: true
            focus: true
            model: root.messages
            reuseItems: true
            cacheBuffer: 600
            boundsBehavior: Flickable.StopAtBounds
            keyNavigationEnabled: false
            ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

            delegate: Item {
                id: row
                required property int index
                required property string messageId
                required property string subject
                required property string sender
                required property string dateText
                required property bool isRead
                required property bool isFlagged
                required property bool isAnswered
                required property bool hasAttachments
                required property string snippet
                required property string snippetRich

                readonly property bool current: row.messageId === root.currentMessageId

                width: ListView.view.width
                height: Theme.rowHeight

                Rectangle {
                    anchors.fill: parent
                    anchors.leftMargin: Theme.s1
                    anchors.rightMargin: Theme.s1
                    anchors.topMargin: 1
                    anchors.bottomMargin: 1
                    radius: Theme.radius
                    color: row.current
                        ? (list.activeFocus ? Theme.selection : Theme.selectionInactive)
                        : hover.hovered ? Theme.surface : "transparent"
                }

                // Unread dot
                Rectangle {
                    x: Theme.s3; y: 15
                    width: 8; height: 8; radius: 4
                    color: Theme.unreadDot
                    visible: !row.isRead
                }

                Text {
                    id: dateLabel
                    anchors { right: parent.right; rightMargin: Theme.s3; top: parent.top; topMargin: 10 }
                    text: row.dateText
                    color: row.isRead ? Theme.textFaint : Theme.textMuted
                    font.pixelSize: Theme.fontSmall
                }
                Text {
                    id: indicators
                    anchors { right: dateLabel.left; rightMargin: Theme.s2; verticalCenter: dateLabel.verticalCenter }
                    text: (row.isAnswered ? "↩ " : "") + (row.hasAttachments ? "⧉ " : "") + (row.isFlagged ? "★" : "")
                    color: row.isFlagged ? Theme.flag : Theme.textMuted
                    font.pixelSize: Theme.fontSmall
                }
                Text {
                    anchors {
                        left: parent.left; leftMargin: 26
                        right: indicators.left; rightMargin: Theme.s2
                        verticalCenter: dateLabel.verticalCenter
                    }
                    text: row.sender
                    color: Theme.text
                    font.pixelSize: Theme.fontBody
                    font.bold: !row.isRead
                    elide: Text.ElideRight
                }
                Text {
                    anchors { left: parent.left; leftMargin: 26; right: parent.right; rightMargin: Theme.s3; top: parent.top; topMargin: 29 }
                    text: row.subject
                    color: row.isRead ? Theme.textMuted : Theme.text
                    font.pixelSize: Theme.fontBody
                    font.bold: !row.isRead
                    elide: Text.ElideRight
                }
                Text {
                    anchors { left: parent.left; leftMargin: 26; right: parent.right; rightMargin: Theme.s3; top: parent.top; topMargin: 48 }
                    text: root.searching ? row.snippetRich : row.snippet
                    textFormat: root.searching ? Text.StyledText : Text.PlainText
                    color: Theme.textFaint
                    font.pixelSize: Theme.fontSmall
                    elide: Text.ElideRight
                    maximumLineCount: 1
                }
                Rectangle {
                    anchors { left: parent.left; right: parent.right; bottom: parent.bottom; leftMargin: 26 }
                    height: 1
                    color: Theme.border
                    opacity: 0.5
                    visible: !row.current
                }

                HoverHandler { id: hover }
                TapHandler {
                    onTapped: {
                        root.selectRow(row.index)
                        list.forceActiveFocus()
                    }
                }
            }

            // Empty state
            Column {
                anchors.centerIn: parent
                spacing: Theme.s2
                visible: list.count === 0 && !root.messages.loading
                Text {
                    anchors.horizontalCenter: parent.horizontalCenter
                    text: root.searching
                          ? (root.messages.unreadOnly
                             ? "No unread results for “" + root.messages.searchQuery + "”"
                             : "No results for “" + root.messages.searchQuery + "”")
                          : (root.messages.unreadOnly ? "No unread messages" : "No messages")
                    color: Theme.textMuted
                    font.pixelSize: Theme.fontBody
                }
            }
        }
    }
}
