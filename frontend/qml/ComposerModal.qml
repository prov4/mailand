pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Dialogs
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
    function openNewTo(accountId: string, to: string) {
        prepare()
        c.initNewTo(accountId, to)
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
    // Debug (--open-composer type:TEXT): type into To as a user would.
    function debugTypeTo(text: string) {
        toField.forceActiveFocus()
        toField.text = text
        toField.cursorPosition = text.length
        toField.textEdited()
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
    Shortcut {
        sequence: "Ctrl+Shift+A"
        enabled: root.opened && !confirm.opened && !root.busy
        onActivated: attachDialog.open()
    }

    FileDialog {
        id: attachDialog
        title: "Attach files"
        fileMode: FileDialog.OpenFiles
        onAccepted: c.addAttachments(selectedFiles)
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
    component AddrField: AddressField {
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

    contentItem: Item {
        ColumnLayout {
            anchors.fill: parent
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
            AddrField {
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
            AddrField {
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
            AddrField {
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

        // Attachments (forwarded read-only chips + removable user-attached chips)
        Flow {
            Layout.fillWidth: true
            spacing: Theme.s2
            visible: (c.mode === "forward" && attachRepeater.count > 0) || userAttachRepeater.count > 0
            Repeater {
                id: attachRepeater
                model: c.mode === "forward" ? c.forwardAttachments : []
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
            Repeater {
                id: userAttachRepeater
                model: c.attachments
                delegate: Rectangle {
                    id: userChip
                    required property var modelData
                    required property int index
                    implicitWidth: userChipRow.implicitWidth + Theme.s3 + Theme.s2
                    implicitHeight: userChipRow.implicitHeight + Theme.s2
                    radius: Theme.radius
                    color: Theme.surfaceAlt
                    border.color: Theme.border
                    RowLayout {
                        id: userChipRow
                        anchors.centerIn: parent
                        spacing: Theme.s2
                        Text {
                            text: userChip.modelData.filename + "  (" + userChip.modelData.sizeText + ")"
                            color: Theme.text
                            font.pixelSize: Theme.fontSmall
                        }
                        Text {
                            text: "×"
                            color: removeArea.containsMouse ? Theme.danger : Theme.textMuted
                            font.pixelSize: Theme.fontBody
                            font.bold: true
                            MouseArea {
                                id: removeArea
                                anchors.fill: parent
                                anchors.margins: -4
                                hoverEnabled: true
                                enabled: !root.busy
                                cursorShape: Qt.PointingHandCursor
                                onClicked: c.removeAttachment(userChip.index)
                            }
                        }
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
            Button {
                id: attachBtn
                text: "Attach…"
                enabled: !root.busy
                focusPolicy: Qt.TabFocus
                onClicked: attachDialog.open()
                contentItem: Label {
                    text: attachBtn.text
                    color: Theme.text
                    font.pixelSize: Theme.fontSmall
                    horizontalAlignment: Text.AlignHCenter
                    verticalAlignment: Text.AlignVCenter
                }
                background: Rectangle {
                    implicitWidth: 76
                    implicitHeight: 30
                    radius: Theme.radius
                    color: Theme.surfaceAlt
                    opacity: attachBtn.enabled ? 1 : 0.5
                    border.color: attachBtn.activeFocus ? Theme.accent : Theme.border
                }
            }
            Label {
                Layout.fillWidth: true
                text: "Ctrl+Enter to send · Ctrl+Shift+A attach · Esc to close"
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

        DropArea {
            id: fileDrop
            anchors.fill: parent
            z: 100
            enabled: !root.busy
            onEntered: function(drag) {
                if (drag.hasUrls)
                    drag.accept(Qt.CopyAction)
                else
                    drag.accepted = false
            }
            onDropped: function(drop) {
                if (drop.hasUrls) {
                    drop.accept(Qt.CopyAction)
                    c.addAttachments(drop.urls)
                }
            }

            Rectangle {
                anchors.fill: parent
                visible: fileDrop.containsDrag
                radius: Theme.radiusLarge
                color: Qt.rgba(0, 0, 0, 0.55)
                border.color: Theme.accent
                border.width: 2
                Label {
                    anchors.centerIn: parent
                    text: "Drop files to attach"
                    color: Theme.text
                    font.pixelSize: Theme.fontTitle
                    font.bold: true
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
