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
    property string view: "mail" // "mail" | "calendar" | "contacts"

    // Plain-letter shortcuts are inactive while typing in a search field or while a modal is open.
    readonly property bool keysEnabled: !listPane.searchActive && !contactsView.searchActive
                                        && !composer.opened && !help.opened && !eventPopup.opened
                                        && !contactEditor.opened
    readonly property bool mailKeys: keysEnabled && view === "mail"
    readonly property bool calendarKeys: keysEnabled && view === "calendar"
    readonly property bool contactKeys: keysEnabled && view === "contacts"

    function showView(v: string) {
        view = v
        if (v === "mail")
            listPane.focusList()
        else
            stack.forceActiveFocus()
    }

    // Selects row 0 of the current list and opens it (used by screenshots).
    function openFirstMessage() {
        if (!listPane.selectRow(0))
            pendingOpenFirst = true
    }

    // Debug (--open-composer): mode is new | reply | replyAll | forward.
    // Debug (--view calendar --open-composer invite): show the first pending invitation.
    function debugOpenInvite() {
        for (let i = 0; i < 42; ++i) {
            const evs = calendarModel.eventsOn(calendarModel.gridDate(i), calendarModel.revision)
            for (const ev of evs)
                if (ev.canRespond && ev.myStatus === "NEEDS-ACTION") {
                    calendarModel.select(calendarModel.gridDate(i))
                    eventPopup.show(ev.id)
                    return
                }
        }
    }
    function debugOpenComposer(mode: string) {
        if (mode === "invite")
            return debugOpenInvite()
        if (mode === "contact")
            return contactEditor.openEdit(contactsModel.get(0))
        if (mode === "new")
            composer.openNew(accountId)
        else if (mode.startsWith("type:")) {
            composer.openNew(accountId)
            composer.debugTypeTo(mode.substring(5))
        }
        else if (mode === "forward")
            composer.openForward(accountId, messageView.messageId)
        else
            composer.openReply(accountId, messageView.messageId, mode === "replyAll")
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
    CalendarModel {
        id: calendarModel
    }
    ContactsModel {
        id: contactsModel
    }

    Connections {
        target: Rpc
        function onAccountsChanged() { root.ensureAccount() }
        function onErrorOccurred(message: string) { toast.show(message, true) }
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
    Shortcut { sequence: "j"; enabled: root.mailKeys; onActivated: listPane.next() }
    Shortcut { sequence: "k"; enabled: root.mailKeys; onActivated: listPane.previous() }
    Shortcut { sequence: "g,g"; enabled: root.mailKeys; onActivated: listPane.first() }
    Shortcut { sequence: "Shift+G"; enabled: root.mailKeys; onActivated: listPane.last() }
    Shortcut { sequence: "Shift+J"; enabled: root.mailKeys; onActivated: root.stepFolder(1) }
    Shortcut { sequence: "Shift+K"; enabled: root.mailKeys; onActivated: root.stepFolder(-1) }
    Shortcut {
        sequences: ["Return", "Enter", "l"]
        enabled: root.mailKeys && messageView.valid
        onActivated: readerPane.focusContent()
    }
    Shortcut {
        sequences: ["h", "Escape"]
        enabled: root.mailKeys
        onActivated: listPane.focusList()
    }
    Shortcut { sequence: "/"; enabled: root.mailKeys; onActivated: listPane.focusSearch() }
    Shortcut { sequence: "c"; enabled: root.keysEnabled && root.view !== "contacts"; onActivated: composer.openNew(root.accountId) }
    Shortcut {
        sequence: "r"
        enabled: root.mailKeys && messageView.valid
        onActivated: composer.openReply(root.accountId, messageView.messageId, false)
    }
    Shortcut {
        sequence: "Shift+R"
        enabled: root.mailKeys && messageView.valid
        onActivated: composer.openReply(root.accountId, messageView.messageId, true)
    }
    Shortcut {
        sequence: "f"
        enabled: root.mailKeys && messageView.valid
        onActivated: composer.openForward(root.accountId, messageView.messageId)
    }
    Shortcut {
        sequence: "m"
        enabled: root.mailKeys
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
        enabled: root.mailKeys
        onActivated: {
            const row = listPane.currentRow()
            if (row >= 0)
                messageModel.toggleFlagged(row)
        }
    }
    Shortcut { sequences: ["?", "Shift+?"]; enabled: root.keysEnabled; onActivated: help.open() }
    Shortcut { sequences: ["F5", "Ctrl+R"]; onActivated: Rpc.triggerSync("") } // = Send/Receive
    Shortcut { sequence: "Ctrl+1"; enabled: !composer.opened; onActivated: root.showView("mail") }
    Shortcut { sequence: "Ctrl+2"; enabled: !composer.opened; onActivated: root.showView("calendar") }
    Shortcut { sequence: "Ctrl+3"; enabled: !composer.opened; onActivated: root.showView("contacts") }

    // Calendar: h/l day, j/k week, n/p month, t today (arrows and PgUp/PgDn too).
    Shortcut { sequences: ["h", "Left"]; enabled: root.calendarKeys; onActivated: calendarModel.moveDays(-1) }
    Shortcut { sequences: ["l", "Right"]; enabled: root.calendarKeys; onActivated: calendarModel.moveDays(1) }
    Shortcut { sequences: ["j", "Down"]; enabled: root.calendarKeys; onActivated: calendarModel.moveDays(7) }
    Shortcut { sequences: ["k", "Up"]; enabled: root.calendarKeys; onActivated: calendarModel.moveDays(-7) }
    Shortcut { sequences: ["n", "PgDown"]; enabled: root.calendarKeys; onActivated: calendarModel.moveMonths(1) }
    Shortcut { sequences: ["p", "PgUp"]; enabled: root.calendarKeys; onActivated: calendarModel.moveMonths(-1) }
    Shortcut { sequence: "t"; enabled: root.calendarKeys; onActivated: calendarModel.goToday() }
    Shortcut { sequences: ["Return", "Enter"]; enabled: root.calendarKeys; onActivated: calendarView.openFirst() }

    // Contacts: j/k move, / search, c or Enter writes to the selected contact.
    Shortcut { sequences: ["j", "Down"]; enabled: root.contactKeys; onActivated: contactsView.next() }
    Shortcut { sequences: ["k", "Up"]; enabled: root.contactKeys; onActivated: contactsView.previous() }
    Shortcut { sequence: "/"; enabled: root.contactKeys; onActivated: contactsView.focusSearch() }
    Shortcut { sequences: ["c", "Return", "Enter"]; enabled: root.contactKeys; onActivated: contactsView.composeToCurrent() }
    Shortcut { sequence: "n"; enabled: root.contactKeys; onActivated: contactEditor.openNew(root.accountId, "", "") }
    Shortcut { sequence: "e"; enabled: root.contactKeys; onActivated: contactsView.editCurrent() }
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
                calendar: calendarModel
                view: root.view
                accountId: root.accountId
                folderId: root.folderId
                onAccountSelected: id => root.selectAccount(id)
                onFolderSelected: id => root.selectFolder(id)
                onViewSelected: v => root.showView(v)
            }

            StackLayout {
                id: stack
                SplitView.fillWidth: true
                currentIndex: root.view === "calendar" ? 1 : root.view === "contacts" ? 2 : 0

                SplitView {
                    orientation: Qt.Horizontal
                    handle: Rectangle {
                        implicitWidth: 1
                        color: Theme.border
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
                        onAddSenderToContacts: (name, email) => contactEditor.openNew(root.accountId, name, email)
                    }
                }

                CalendarView {
                    id: calendarView
                    calendar: calendarModel
                    onEventActivated: id => eventPopup.show(id)
                }

                ContactsView {
                    id: contactsView
                    contacts: contactsModel
                    onComposeTo: (accountId, address) => composer.openNewTo(accountId.length > 0 ? accountId : root.accountId, address)
                    onNewContact: contactEditor.openNew(root.accountId, "", "")
                    onEditContact: c => contactEditor.openEdit(c)
                    onOpenMessage: id => { root.showView("mail"); root.openMessage(id) }
                }
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
                    text: Rpc.syncRemaining > 0
                          ? "Syncing… " + Rpc.syncRemaining.toLocaleString(Qt.locale(), 'f', 0) + " left"
                          : "Syncing…"
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
                    text: root.view === "calendar" ? (calendarModel.eventCount + " events this view")
                        : root.view === "contacts" ? (contactsModel.count + " contacts")
                        : messageModel.totalCount > 0 ? messageModel.totalCount + " messages" : ""
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
        property bool isError: true
        function show(msg: string, error: bool) {
            message = msg
            isError = error
            opacity = 1
            hideTimer.restart()
        }
        anchors { right: parent.right; bottom: parent.bottom; rightMargin: Theme.s4; bottomMargin: Theme.statusHeight + Theme.s3 }
        width: Math.min(420, toastText.implicitWidth + 2 * Theme.s4)
        height: toastText.implicitHeight + 2 * Theme.s3
        radius: Theme.radius
        color: Theme.surfaceAlt
        border.color: toast.isError ? Theme.danger : Theme.success
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

    ComposerModal {
        id: composer
        onSent: toast.show("Message sent", false)
    }
    HelpOverlay { id: help }
    EventPopup {
        id: eventPopup
        calendar: calendarModel
    }
    ContactEditor {
        id: contactEditor
        contacts: contactsModel
    }
    Connections {
        target: contactsModel
        function onContactSaved() { toast.show("Contact saved", false) }
        function onContactDeleted() { toast.show("Contact deleted", false) }
    }
}
