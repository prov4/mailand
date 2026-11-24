pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Mailand

Popup {
    id: root

    readonly property alias composer: c
    readonly property bool busy: c.sending || c.loading
    property bool showCcBcc: false

    signal sent()

    function openNew(accountId: string) {
        prepare()
        c.initNew(accountId)
        open()
    }
    function openReply(accountId: string, messageId: string, replyAll: bool) {
        prepare()
        c.accountId = accountId
        c.initReply(messageId, replyAll)
        open()
    }
    function openForward(accountId: string, messageId: string) {
        prepare()
        c.accountId = accountId
        c.initForward(messageId)
        open()
    }
    function prepare() {
        showCcBcc = false
        confirm.close()
    }
    function requestClose() {
        if (c.sending)
            return
        if (c.dirty)
            confirm.open()
        else
            close()
    }
    function focusStart() {
        if (c.mode === "reply" || c.mode === "replyAll") {
            body.forceActiveFocus()
            body.cursorPosition = 0
        } else {
            toField.forceActiveFocus()
        }
    }

    Composer {
        id: c
        onReady: {
            if (c.cc.length > 0 || c.bcc.length > 0)
                root.showCcBcc = true
            root.focusStart()
        }
        onSent: {
            root.close()
            root.sent()
        }
        onFailed: root.focusStart()
    }

    modal: true
    focus: true
    closePolicy: Popup.NoAutoClose
    anchors.centerIn: Overlay.overlay
    width: Overlay.overlay ? Math.min(Overlay.overlay.width - 32, Math.max(600, Overlay.overlay.width * 0.7)) : 800
    height: Overlay.overlay ? Math.min(Overlay.overlay.height - 32, Math.max(420, Overlay.overlay.height * 0.75)) : 600
    padding: Theme.s4

    onOpened: if (!c.loading) focusStart()

    Shortcut {
        sequence: "Escape"
        enabled: root.opened && !confirm.opened
        onActivated: root.requestClose()
    }
    Shortcut {
        sequences: ["Ctrl+Return", "Ctrl+Enter"]
        enabled: root.opened && !confirm.opened
        onActivated: c.send()
    }

    background: Rectangle {
        color: Theme.surface
        radius: Theme.radiusLarge
        border.color: Theme.border
    }
    Overlay.modal: Rectangle { color: "#99000000" }

    component FieldLabel: Label {
        Layout.preferredWidth: 56
        color: Theme.textMuted
        font.pixelSize: Theme.fontSmall
        verticalAlignment: Text.AlignVCenter
    }
    component Field: TextField {
        Layout.fillWidth: true
        color: Theme.text
        selectionColor: Theme.selection
        selectedTextColor: Theme.text
        placeholderTextColor: Theme.textFaint
        font.pixelSize: Theme.fontBody
        leftPadding: Theme.s2
        rightPadding: Theme.s2
        topPadding: Theme.s2
        bottomPadding: Theme.s2
        enabled: !root.busy
        background: Rectangle {
            radius: Theme.radius
            color: Theme.bg
            border.color: parent.activeFocus ? Theme.accent : Theme.border
        }
    }

    contentItem: ColumnLayout {
        spacing: Theme.s2

        RowLayout {
            Layout.fillWidth: true
            Label {
                text: c.mode === "reply" ? "Reply" : c.mode === "replyAll" ? "Reply all"
                      : c.mode === "forward" ? "Forward" : "New message"
                color: Theme.text
                font.pixelSize: Theme.fontTitle
                font.bold: true
                Layout.fillWidth: true
            }
            BusyIndicator {
                Layout.preferredWidth: 20; Layout.preferredHeight: 20
                running: visible
                visible: c.loading
            }
        }

        RowLayout {
            Layout.fillWidth: true
            FieldLabel { text: "From" }
            Label {
                Layout.fillWidth: true
                text: c.fromText
                color: Theme.textMuted
                font.pixelSize: Theme.fontBody
                elide: Text.ElideRight
            }
        }

        RowLayout {
            Layout.fillWidth: true
            FieldLabel { text: "To" }
            Field {
                id: toField
                text: c.to
                placeholderText: "name@example.com, …"
                onTextEdited: c.to = text
                KeyNavigation.tab: root.showCcBcc ? ccField : subjectField
            }
            Button {
                id: ccToggle
                visible: !root.showCcBcc
                text: "Cc/Bcc"
                flat: true
                focusPolicy: Qt.NoFocus
                enabled: !root.busy
                onClicked: { root.showCcBcc = true; ccField.forceActiveFocus() }
                contentItem: Label {
                    text: ccToggle.text
                    color: Theme.accent
                    font.pixelSize: Theme.fontSmall
                    horizontalAlignment: Text.AlignHCenter
                    verticalAlignment: Text.AlignVCenter
                }
                background: Item {}
            }
        }
        RowLayout {
            Layout.fillWidth: true
            visible: root.showCcBcc
            FieldLabel { text: "Cc" }
            Field {
                id: ccField
                text: c.cc
                onTextEdited: c.cc = text
                KeyNavigation.tab: bccField
                KeyNavigation.backtab: toField
            }
        }
        RowLayout {
            Layout.fillWidth: true
            visible: root.showCcBcc
            FieldLabel { text: "Bcc" }
            Field {
                id: bccField
                text: c.bcc
                onTextEdited: c.bcc = text
                KeyNavigation.tab: subjectField
                KeyNavigation.backtab: ccField
            }
        }
        RowLayout {
            Layout.fillWidth: true
            FieldLabel { text: "Subject" }
            Field {
                id: subjectField
                text: c.subject
                onTextEdited: c.subject = text
                KeyNavigation.tab: body
                KeyNavigation.backtab: root.showCcBcc ? bccField : toField
            }
        }

        // Forward attachments (read-only chips)
        Flow {
            Layout.fillWidth: true
            spacing: Theme.s2
            visible: c.mode === "forward" && attachRepeater.count > 0
            Repeater {
                id: attachRepeater
                model: c.forwardAttachments
                delegate: Rectangle {
                    id: chip
                    required property var modelData
                    implicitWidth: chipText.implicitWidth + 2 * Theme.s3
                    implicitHeight: chipText.implicitHeight + Theme.s2
                    radius: Theme.radius
                    color: Theme.surfaceAlt
                    border.color: Theme.border
                    Text {
                        id: chipText
                        anchors.centerIn: parent
                        text: chip.modelData.filename + "  (" + chip.modelData.sizeText + ")"
                        color: Theme.text
                        font.pixelSize: Theme.fontSmall
                    }
                }
            }
        }

        ScrollView {
            id: bodyScroll
            Layout.fillWidth: true
            Layout.fillHeight: true
            clip: true
            background: Rectangle {
                radius: Theme.radius
                color: Theme.bg
                border.color: body.activeFocus ? Theme.accent : Theme.border
            }
            TextArea {
                id: body
                text: c.body
                onTextChanged: if (text !== c.body) c.body = text
                enabled: !root.busy
                wrapMode: TextEdit.Wrap
                color: Theme.text
                selectionColor: Theme.selection
                selectedTextColor: Theme.text
                font.family: Theme.monoFamily
                font.pixelSize: Theme.fontBody
                padding: Theme.s3
                background: null
                KeyNavigation.priority: KeyNavigation.BeforeItem
                KeyNavigation.tab: toField
                KeyNavigation.backtab: subjectField
            }
        }

        Text {
            Layout.fillWidth: true
            visible: c.errorText.length > 0
            text: c.errorText
            color: Theme.danger
            wrapMode: Text.Wrap
            font.pixelSize: Theme.fontSmall
        }

        RowLayout {
            Layout.fillWidth: true
            spacing: Theme.s3
            Label {
                Layout.fillWidth: true
                text: "Ctrl+Enter to send · Esc to close"
                color: Theme.textFaint
                font.pixelSize: Theme.fontSmall
            }
            BusyIndicator {
                Layout.preferredWidth: 20; Layout.preferredHeight: 20
                running: visible
                visible: c.sending
            }
            Button {
                id: sendBtn
                text: c.sending ? "Sending…" : "Send"
                enabled: !root.busy
                focusPolicy: Qt.TabFocus
                onClicked: c.send()
                contentItem: Label {
                    text: sendBtn.text
                    color: Theme.accentText
                    font.pixelSize: Theme.fontBody
                    font.bold: true
                    horizontalAlignment: Text.AlignHCenter
                    verticalAlignment: Text.AlignVCenter
                }
                background: Rectangle {
                    implicitWidth: 88
                    implicitHeight: 32
                    radius: Theme.radius
                    color: Theme.accent
                    opacity: sendBtn.enabled ? 1 : 0.5
                    border.color: sendBtn.activeFocus ? Theme.text : "transparent"
                }
            }
        }
    }

    // "Discard draft?" confirmation
    Popup {
        id: confirm
        parent: Overlay.overlay
        anchors.centerIn: Overlay.overlay
        modal: true
        focus: true
        padding: Theme.s4
        closePolicy: Popup.CloseOnEscape
        onOpened: keepBtn.forceActiveFocus()
        onClosed: if (root.opened) root.focusStart()

        background: Rectangle {
            color: Theme.surfaceAlt
            radius: Theme.radiusLarge
            border.color: Theme.border
        }
        Overlay.modal: Rectangle { color: "#66000000" }

        contentItem: ColumnLayout {
            spacing: Theme.s3
            Label {
                text: "Discard draft?"
                color: Theme.text
                font.pixelSize: Theme.fontBody
                font.bold: true
            }
            RowLayout {
                spacing: Theme.s2
                Button {
                    id: discardBtn
                    text: "Discard"
                    onClicked: { confirm.close(); root.close() }
                    Keys.onReturnPressed: clicked()
                    KeyNavigation.right: keepBtn
                    contentItem: Label {
                        text: discardBtn.text
                        color: Theme.danger
                        font.pixelSize: Theme.fontBody
                        horizontalAlignment: Text.AlignHCenter
                    }
                    background: Rectangle {
                        implicitWidth: 96; implicitHeight: 30
                        radius: Theme.radius
                        color: Theme.bg
                        border.color: discardBtn.activeFocus ? Theme.accent : Theme.border
                    }
                }
                Button {
                    id: keepBtn
                    text: "Keep editing"
                    onClicked: confirm.close()
                    Keys.onReturnPressed: clicked()
                    KeyNavigation.left: discardBtn
                    contentItem: Label {
                        text: keepBtn.text
                        color: Theme.text
                        font.pixelSize: Theme.fontBody
                        horizontalAlignment: Text.AlignHCenter
                    }
                    background: Rectangle {
                        implicitWidth: 110; implicitHeight: 30
                        radius: Theme.radius
                        color: Theme.bg
                        border.color: keepBtn.activeFocus ? Theme.accent : Theme.border
                    }
                }
            }
        }
    }
}
