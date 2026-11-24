pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Mailand

ApplicationWindow {
    id: root
    visible: true
    width: 1280
    height: 800
    minimumWidth: 800
    minimumHeight: 480
    title: "mailand"
    flags: Qt.FramelessWindowHint | Qt.Window
    color: Theme.bg

    property string accountId
    property string folderId
    property bool pendingOpenFirst: false

    // Plain-letter shortcuts are inactive while typing in search or while a modal is open.
    readonly property bool keysEnabled: !listPane.searchActive && !composer.opened && !help.opened

    // Selects row 0 of the current list and opens it (used by screenshots).
    function openFirstMessage() {
        if (!listPane.selectRow(0))
            pendingOpenFirst = true
    }

    function selectAccount(id: string) {
        if (id === accountId)
            return
        accountId = id
        folderId = ""
        listPane.clearSearch()
        listPane.clearSelection()
        messageView.messageId = ""
    }
    function selectFolder(id: string) {
        if (id.length === 0 || id === folderId)
            return
        listPane.clearSearch()
        listPane.clearSelection()
        messageView.messageId = ""
        folderId = id
    }
    function stepFolder(delta: int) {
        const row = folderModel.indexOfFolder(folderId) + delta
        if (row >= 0 && row < folderModel.count)
            selectFolder(folderModel.folderIdAt(row))
    }
    function openMessage(id: string) {
        MailWeb.remoteAllowed = false
        messageView.messageId = id
        readTimer.restart()
    }
    function openComposer(mode: string) {
        composer.mode = mode
        composer.open()
    }

    FolderModel {
        id: folderModel
        accountId: root.accountId
        onCountChanged: {
            // Default selection: the Inbox (always first in FolderModel order).
            if (root.folderId.length === 0 && count > 0)
                root.selectFolder(folderIdAt(0))
        }
    }
    MessageModel {
        id: messageModel
        folderId: root.folderId
        accountId: root.accountId
        onCountChanged: {
            if (root.pendingOpenFirst && count > 0) {
                root.pendingOpenFirst = false
                listPane.selectRow(0)
            }
        }
    }
    MessageView {
        id: messageView
    }

    Connections {
        target: Rpc
        function onAccountsChanged() { root.ensureAccount() }
        function onErrorOccurred(message: string) { toast.show(message) }
    }
    function ensureAccount() {
        const a = Rpc.accounts
        if (a.length === 0)
            return
        for (let i = 0; i < a.length; ++i)
            if (a[i].id === accountId)
                return
        selectAccount(a[0].id)
    }
    Component.onCompleted: ensureAccount()

    // Mark as read once the message has been on screen for 600 ms.
    Timer {
        id: readTimer
        interval: 600
        onTriggered: {
            const row = messageModel.indexOfMessage(messageView.messageId)
            if (row >= 0 && !messageModel.get(row).isRead)
                messageModel.setRead(row, true)
        }
    }

    // ---- Keyboard ---------------------------------------------------------------------------
    // j/k move between messages everywhere (also while the reader has focus).
    Shortcut { sequence: "j"; enabled: root.keysEnabled; onActivated: listPane.next() }
    Shortcut { sequence: "k"; enabled: root.keysEnabled; onActivated: listPane.previous() }
    Shortcut { sequence: "g,g"; enabled: root.keysEnabled; onActivated: listPane.first() }
    Shortcut { sequence: "Shift+G"; enabled: root.keysEnabled; onActivated: listPane.last() }
    Shortcut { sequence: "Shift+J"; enabled: root.keysEnabled; onActivated: root.stepFolder(1) }
    Shortcut { sequence: "Shift+K"; enabled: root.keysEnabled; onActivated: root.stepFolder(-1) }
    Shortcut {
        sequences: ["Return", "Enter", "l"]
        enabled: root.keysEnabled && messageView.valid
        onActivated: readerPane.focusContent()
    }
    Shortcut {
        sequences: ["h", "Escape"]
        enabled: root.keysEnabled
        onActivated: listPane.focusList()
    }
    Shortcut { sequence: "/"; enabled: root.keysEnabled; onActivated: listPane.focusSearch() }
    Shortcut { sequence: "c"; enabled: root.keysEnabled; onActivated: root.openComposer("compose") }
    Shortcut {
        sequence: "r"
        enabled: root.keysEnabled && messageView.messageId.length > 0
        onActivated: root.openComposer("reply")
    }
    Shortcut {
        sequence: "m"
        enabled: root.keysEnabled
        onActivated: {
            const row = listPane.currentRow()
            if (row >= 0) {
                readTimer.stop()
                messageModel.toggleRead(row)
            }
        }
    }
    Shortcut {
        sequence: "s"
        enabled: root.keysEnabled
        onActivated: {
            const row = listPane.currentRow()
            if (row >= 0)
                messageModel.toggleFlagged(row)
        }
    }
    Shortcut { sequences: ["?", "Shift+?"]; enabled: root.keysEnabled; onActivated: help.open() }
    Shortcut { sequences: ["F5", "Ctrl+R"]; onActivated: Rpc.triggerSync(root.accountId) }
    Shortcut { sequence: "Ctrl+Q"; onActivated: Qt.quit() }

    // ---- Layout -----------------------------------------------------------------------------
    ColumnLayout {
        anchors.fill: parent
        spacing: 0

        // Reauth banner
        Rectangle {
            Layout.fillWidth: true
            Layout.preferredHeight: 32
            visible: Rpc.reauthAccount.length > 0
            color: Theme.danger
            Text {
                anchors.centerIn: parent
                text: "Account needs re-authentication. Run: mailand-daemon login " + Rpc.reauthAccount
                color: Theme.accentText
                font.pixelSize: Theme.fontSmall
                font.bold: true
            }
        }

        SplitView {
            Layout.fillWidth: true
            Layout.fillHeight: true
            orientation: Qt.Horizontal

            handle: Rectangle {
                implicitWidth: 1
                color: Theme.border
            }

            SidebarPane {
                SplitView.preferredWidth: Theme.sidebarWidth
                SplitView.minimumWidth: 160
                SplitView.maximumWidth: 360
                folders: folderModel
                accountId: root.accountId
                folderId: root.folderId
                onAccountSelected: id => root.selectAccount(id)
                onFolderSelected: id => root.selectFolder(id)
            }

            MessageListPane {
                id: listPane
                SplitView.preferredWidth: Theme.listWidth
                SplitView.minimumWidth: 280
                SplitView.maximumWidth: 640
                messages: messageModel
                accountId: root.accountId
                focus: true
                onMessageActivated: id => root.openMessage(id)
            }

            ReaderPane {
                id: readerPane
                SplitView.fillWidth: true
                SplitView.minimumWidth: 320
                message: messageView
            }
        }

        // Status bar
        Rectangle {
            Layout.fillWidth: true
            Layout.preferredHeight: Theme.statusHeight
            color: Theme.surface
            Rectangle { width: parent.width; height: 1; color: Theme.border }

            RowLayout {
                anchors.fill: parent
                anchors.leftMargin: Theme.s3
                anchors.rightMargin: Theme.s3
                spacing: Theme.s2

                Rectangle {
                    Layout.preferredWidth: 8; Layout.preferredHeight: 8; radius: 4
                    color: Rpc.connected ? Theme.success : Theme.danger
                }
                Text {
                    text: Rpc.connected ? "Connected" : "Daemon not connected"
                    color: Theme.textMuted
                    font.pixelSize: 11
                }
                BusyIndicator {
                    Layout.preferredWidth: 16; Layout.preferredHeight: 16
                    running: visible
                    visible: Rpc.syncing
                }
                Text {
                    visible: Rpc.syncing
                    text: "Syncing…"
                    color: Theme.textMuted
                    font.pixelSize: 11
                }
                Text {
                    Layout.fillWidth: true
                    text: Rpc.lastError
                    color: Theme.danger
                    font.pixelSize: 11
                    elide: Text.ElideRight
                }
                Text {
                    text: messageModel.totalCount > 0 ? messageModel.totalCount + " messages" : ""
                    color: Theme.textFaint
                    font.pixelSize: 11
                }
                Text {
                    text: "? help"
                    color: Theme.textFaint
                    font.pixelSize: 11
                }
            }
        }
    }

    // Error toast
    Rectangle {
        id: toast
        property string message
        function show(msg: string) {
            message = msg
            opacity = 1
            hideTimer.restart()
        }
        anchors { right: parent.right; bottom: parent.bottom; rightMargin: Theme.s4; bottomMargin: Theme.statusHeight + Theme.s3 }
        width: Math.min(420, toastText.implicitWidth + 2 * Theme.s4)
        height: toastText.implicitHeight + 2 * Theme.s3
        radius: Theme.radius
        color: Theme.surfaceAlt
        border.color: Theme.danger
        opacity: 0
        visible: opacity > 0
        Behavior on opacity { NumberAnimation { duration: 150 } }
        Text {
            id: toastText
            anchors.fill: parent
            anchors.margins: Theme.s3
            text: toast.message
            color: Theme.text
            wrapMode: Text.Wrap
            font.pixelSize: Theme.fontSmall
        }
        Timer { id: hideTimer; interval: 5000; onTriggered: toast.opacity = 0 }
    }

    ComposerModal { id: composer }
    HelpOverlay { id: help }
}
