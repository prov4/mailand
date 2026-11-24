pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Mailand

// Create / edit / delete a contact. Ctrl+Enter saves, Esc cancels.
Popup {
    id: root
    required property ContactsModel contacts
    property string contactId      // "" = new contact
    property string accountId      // new contacts: whose address book
    property bool confirmDelete: false
    property string error
    readonly property bool isNew: contactId.length === 0

    // New contact, optionally prefilled ("Add sender to contacts").
    function openNew(account: string, name: string, email: string) {
        reset("", account)
        nameField.text = name
        emailsField.text = email
        open()
        // An existing contact with this address is edited instead of duplicated.
        if (email.length > 0)
            lookup.query = email
    }
    function openEdit(c: var) {
        reset(c.contactId, c.accountId)
        fill(c)
        open()
    }
    function reset(id: string, account: string) {
        contactId = id
        accountId = account
        confirmDelete = false
        error = ""
        lookup.query = ""
        nameField.text = ""; orgField.text = ""; emailsField.text = ""; phonesField.text = ""
    }
    function fill(c: var) {
        nameField.text = c.fullName ?? ""
        orgField.text = c.organization ?? ""
        emailsField.text = (c.emails ?? []).join("\n")
        phonesField.text = (c.phones ?? []).join("\n")
    }
    function lines(t: string): var {
        return t.split(/[\n,;]/).map(s => s.trim()).filter(s => s.length > 0)
    }
    function save() {
        if (contacts.saving)
            return
        error = ""
        contacts.save({
            contactId: contactId, accountId: accountId,
            fullName: nameField.text, organization: orgField.text,
            emails: lines(emailsField.text), phones: lines(phonesField.text)
        })
    }

    ContactsModel {
        id: lookup
        perEmail: true
        limit: 5
        minQueryLength: 3
        onCountChanged: {
            if (!root.isNew || count === 0)
                return
            const want = root.lines(emailsField.text)[0] ?? ""
            for (let i = 0; i < count; ++i) {
                const c = get(i)
                if (c.email.toLowerCase() === want.toLowerCase()) {
                    root.contactId = c.contactId
                    root.accountId = c.accountId
                    const name = nameField.text
                    root.fill(c)
                    if ((c.fullName ?? "").length === 0 && name.length > 0)
                        nameField.text = name
                    root.error = "Already in your contacts: editing the existing entry."
                    return
                }
            }
        }
    }
    Connections {
        target: root.contacts
        function onContactSaved() { if (root.opened) root.close() }
        function onContactDeleted() { if (root.opened) root.close() }
        function onEditFailed(message: string) { if (root.opened) root.error = message }
    }

    modal: true
    focus: true
    anchors.centerIn: Overlay.overlay
    width: Math.min(520, (parent ? parent.width : 520) - 2 * Theme.s5)
    padding: Theme.s5
    closePolicy: Popup.CloseOnEscape
    onOpened: nameField.forceActiveFocus()

    background: Rectangle {
        color: Theme.surface
        radius: Theme.radiusLarge
        border.color: Theme.border
    }
    Overlay.modal: Rectangle { color: "#99000000" }

    Shortcut { sequences: ["Ctrl+Return", "Ctrl+Enter"]; enabled: root.opened; onActivated: root.save() }

    component FieldLabel: Label {
        Layout.preferredWidth: 96
        Layout.alignment: Qt.AlignTop
        topPadding: Theme.s2
        color: Theme.textMuted
        font.pixelSize: Theme.fontSmall
    }
    component Input: TextField {
        Layout.fillWidth: true
        color: Theme.text
        placeholderTextColor: Theme.textFaint
        selectionColor: Theme.selection
        font.pixelSize: Theme.fontBody
        leftPadding: Theme.s2; rightPadding: Theme.s2; topPadding: Theme.s2; bottomPadding: Theme.s2
        enabled: !root.contacts.saving
        background: Rectangle { radius: Theme.radius; color: Theme.bg; border.color: parent.activeFocus ? Theme.accent : Theme.border }
    }
    component MultiInput: TextArea {
        Layout.fillWidth: true
        Layout.preferredHeight: Math.max(64, contentHeight + topPadding + bottomPadding)
        color: Theme.text
        placeholderTextColor: Theme.textFaint
        selectionColor: Theme.selection
        font.pixelSize: Theme.fontBody
        wrapMode: TextArea.NoWrap
        leftPadding: Theme.s2; rightPadding: Theme.s2; topPadding: Theme.s2; bottomPadding: Theme.s2
        enabled: !root.contacts.saving
        KeyNavigation.priority: KeyNavigation.BeforeItem
        background: Rectangle { radius: Theme.radius; color: Theme.bg; border.color: parent.activeFocus ? Theme.accent : Theme.border }
    }
    component ActionButton: Button {
        id: ab
        property color fill: Theme.surfaceAlt
        property color ink: Theme.text
        focusPolicy: Qt.NoFocus
        leftPadding: Theme.s4; rightPadding: Theme.s4; topPadding: Theme.s2; bottomPadding: Theme.s2
        background: Rectangle { radius: Theme.radius; color: ab.down ? Qt.darker(ab.fill, 1.2) : ab.fill; border.color: Theme.border }
        contentItem: Label {
            text: ab.text
            color: ab.ink
            font.pixelSize: Theme.fontBody
            font.bold: true
            horizontalAlignment: Text.AlignHCenter
            verticalAlignment: Text.AlignVCenter
        }
    }

    contentItem: ColumnLayout {
        spacing: Theme.s3

        Label {
            text: root.isNew ? "New contact" : "Edit contact"
            color: Theme.text
            font.pixelSize: Theme.fontTitle
            font.bold: true
        }

        RowLayout {
            Layout.fillWidth: true
            visible: root.isNew && Rpc.accounts.length > 1
            FieldLabel { text: "Save to" }
            ComboBox {
                id: accountBox
                Layout.fillWidth: true
                model: Rpc.accounts
                textRole: "name"
                valueRole: "id"
                currentIndex: Math.max(0, indexOfValue(root.accountId))
                onActivated: root.accountId = currentValue
            }
        }
        RowLayout {
            Layout.fillWidth: true
            FieldLabel { text: "Name" }
            Input { id: nameField; placeholderText: "Ana Horvat"; KeyNavigation.tab: orgField }
        }
        RowLayout {
            Layout.fillWidth: true
            FieldLabel { text: "Organization" }
            Input { id: orgField; KeyNavigation.tab: emailsField; KeyNavigation.backtab: nameField }
        }
        RowLayout {
            Layout.fillWidth: true
            FieldLabel { text: "Email\n(one per line)" }
            MultiInput { id: emailsField; placeholderText: "ana@example.hr"; KeyNavigation.tab: phonesField; KeyNavigation.backtab: orgField }
        }
        RowLayout {
            Layout.fillWidth: true
            FieldLabel { text: "Phone\n(one per line)" }
            MultiInput { id: phonesField; placeholderText: "+385 91 123 4567"; KeyNavigation.backtab: emailsField }
        }

        Label {
            Layout.fillWidth: true
            visible: root.error.length > 0
            text: root.error
            color: root.error.startsWith("Already") ? Theme.textMuted : Theme.danger
            font.pixelSize: Theme.fontSmall
            wrapMode: Text.Wrap
        }

        RowLayout {
            Layout.fillWidth: true
            Layout.topMargin: Theme.s2
            spacing: Theme.s2
            ActionButton {
                visible: !root.isNew && !root.confirmDelete
                text: "Delete"
                ink: Theme.danger
                onClicked: root.confirmDelete = true
            }
            ActionButton {
                visible: root.confirmDelete
                text: "Really delete"
                fill: Theme.danger
                ink: Theme.accentText
                onClicked: root.contacts.remove(root.contactId)
            }
            Item { Layout.fillWidth: true }
            BusyIndicator {
                Layout.preferredWidth: 20; Layout.preferredHeight: 20
                running: visible
                visible: root.contacts.saving
            }
            ActionButton { text: "Cancel"; onClicked: root.close() }
            ActionButton {
                text: "Save  (Ctrl+Enter)"
                fill: Theme.accent
                ink: Theme.accentText
                enabled: !root.contacts.saving
                onClicked: root.save()
            }
        }
    }
}
