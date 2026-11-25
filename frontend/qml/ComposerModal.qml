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
    function sendNow() {
        c.body = rt.plainText()
        c.htmlBody = rt.htmlBody()
        c.send()
    }
    function focusStart() {
        body.cursorPosition = 0
        if (c.mode === "reply" || c.mode === "replyAll") {
            body.forceActiveFocus()
        } else {
            toField.forceActiveFocus()
        }
    }

    property bool syncingBody: false

    RichTextHelper {
        id: rt
        document: body.textDocument
        cursorPosition: body.cursorPosition
        selectionStart: body.selectionStart
        selectionEnd: body.selectionEnd
    }

    Composer {
        id: c
        onReady: {
            if (c.cc.length > 0 || c.bcc.length > 0)
                root.showCcBcc = true
            root.syncingBody = true
            rt.setPlainContent(c.body)
            root.syncingBody = false
            root.focusStart()
        }
        onSignatureEnabledChanged: {
            root.syncingBody = true
            rt.setPlainContent(c.body)
            root.syncingBody = false
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
        onActivated: root.sendNow()
    }
    Shortcut {
        sequence: "Ctrl+Shift+A"
        enabled: root.opened && !confirm.opened && !root.busy
        onActivated: attachDialog.open()
    }
    Shortcut { sequence: "Ctrl+B"; enabled: root.opened && body.activeFocus && !root.busy; onActivated: rt.toggleBold() }
    Shortcut { sequence: "Ctrl+I"; enabled: root.opened && body.activeFocus && !root.busy; onActivated: rt.toggleItalic() }
    Shortcut { sequence: "Ctrl+U"; enabled: root.opened && body.activeFocus && !root.busy; onActivated: rt.toggleUnderline() }

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
            Label {
                visible: c.hasSignature
                text: "Signature:"
                color: Theme.textMuted
                font.pixelSize: Theme.fontSmall
            }
            ComposerSelect {
                id: sigBox
                visible: c.hasSignature
                enabled: !root.busy
                focusPolicy: Qt.NoFocus
                implicitWidth: 100
                model: ["Default", "None"]
                currentIndex: c.signatureEnabled ? 0 : 1
                onActivated: function(idx) { c.signatureEnabled = (idx === 0) }
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

        // Unified editor card: integrated formatting toolbar + body
        Rectangle {
            Layout.fillWidth: true
            Layout.fillHeight: true
            radius: Theme.radius
            color: Theme.bg
            border.color: body.activeFocus ? Theme.accent : Theme.border

            ColumnLayout {
                anchors.fill: parent
                anchors.margins: 1
                spacing: 0

                // Toolbar strip
                Rectangle {
                    Layout.fillWidth: true
                    implicitHeight: fmtBar.implicitHeight + 10
                    color: Theme.surfaceAlt
                    radius: Theme.radius - 1

                    // Square off bottom corners so it meets the hairline cleanly
                    Rectangle {
                        anchors { left: parent.left; right: parent.right; bottom: parent.bottom }
                        height: Theme.radius
                        color: parent.color
                    }

                    Flow {
                        id: fmtBar
                        anchors {
                            left: parent.left; right: parent.right
                            verticalCenter: parent.verticalCenter
                            leftMargin: 8; rightMargin: 8
                        }
                        spacing: 4

                        component ToolSep: Item {
                            implicitWidth: 9
                            implicitHeight: 30
                            Rectangle {
                                anchors.centerIn: parent
                                width: 1
                                height: 18
                                color: Theme.border
                            }
                        }

                        component ToolBtn: Button {
                            id: tb
                            property bool active: false
                            property string symbol: ""
                            focusPolicy: Qt.NoFocus
                            enabled: !root.busy
                            implicitHeight: 30
                            implicitWidth: 30
                            padding: 0
                            contentItem: Item {
                                ComposerIcon {
                                    anchors.centerIn: parent
                                    kind: tb.symbol
                                    ink: tb.active ? Theme.accent : (tb.hovered ? Theme.text : Theme.textMuted)
                                    iconSize: 18
                                }
                            }
                            background: Rectangle {
                                radius: Theme.radius - 1
                                color: tb.active
                                       ? Qt.alpha(Theme.accent, 0.16)
                                       : (tb.hovered ? Qt.alpha(Theme.text, 0.08) : "transparent")
                            }
                        }

                        // Paragraph style
                        ComposerSelect {
                            id: styleBox
                            enabled: !root.busy
                            implicitWidth: 96
                            readonly property var keys: ["body", "h1", "h2", "h3", "quote", "code"]
                            model: ["Body", "Heading 1", "Heading 2", "Heading 3", "Quote", "Code"]
                            currentIndex: Math.max(0, keys.indexOf(rt.blockStyle))
                            onActivated: function(idx) { rt.setBlockStyle(keys[idx]); body.forceActiveFocus() }
                        }

                        // Font family
                        ComposerSelect {
                            id: fontBox
                            enabled: !root.busy
                            implicitWidth: 108
                            model: ["Sans-serif", "Serif", "Monospace", "Helvetica", "Georgia", "Courier"]
                            currentIndex: {
                                const i = model.indexOf(rt.fontFamily)
                                return i >= 0 ? i : 0
                            }
                            onActivated: function(idx) { rt.setFontFamily(model[idx]); body.forceActiveFocus() }
                        }

                        // Font size
                        ComposerSelect {
                            id: sizeBox
                            enabled: !root.busy
                            implicitWidth: 56
                            readonly property var sizes: [11, 12, 14, 16, 18, 24, 32]
                            model: ["11", "12", "14", "16", "18", "24", "32"]
                            currentIndex: {
                                const i = sizes.indexOf(rt.fontSize)
                                return i >= 0 ? i : 2
                            }
                            onActivated: function(idx) { rt.setFontSize(sizes[idx]); body.forceActiveFocus() }
                        }

                        ToolSep {}

                        // Text color swatch + clear formatting
                        Row {
                            spacing: 2
                            Button {
                                id: colorBtn
                                enabled: !root.busy
                                focusPolicy: Qt.NoFocus
                                implicitWidth: 30
                                implicitHeight: 30
                                padding: 0
                                onClicked: colorPop.open()
                                ToolTip.visible: hovered; ToolTip.delay: 400; ToolTip.text: "Text color"
                                background: Rectangle {
                                    radius: Theme.radius - 1
                                    color: colorBtn.hovered ? Qt.alpha(Theme.text, 0.08) : "transparent"
                                    Rectangle {
                                        anchors.centerIn: parent
                                        width: 16; height: 16; radius: 8
                                        color: rt.textColor.valid ? rt.textColor : Theme.text
                                        border.color: Theme.border
                                        border.width: 1
                                    }
                                }
                                Popup {
                                    id: colorPop
                                    y: colorBtn.height + 6
                                    padding: Theme.s2
                                    background: Rectangle {
                                        color: Theme.surface
                                        border.color: Theme.border
                                        radius: Theme.radius
                                    }
                                    contentItem: Row {
                                        spacing: 6
                                        Repeater {
                                            model: ["", "#e6e9ef", "#1a1d23", "#d20f39", "#df8e1d", "#40a02b", "#1e66f5", "#8839ef", "#6c7086"]
                                            delegate: Rectangle {
                                                id: sw
                                                required property string modelData
                                                width: 20; height: 20; radius: 10
                                                color: sw.modelData === "" ? Theme.bg : sw.modelData
                                                border.color: Theme.border
                                                Text {
                                                    anchors.centerIn: parent
                                                    visible: sw.modelData === ""
                                                    text: "A"
                                                    color: Theme.text
                                                    font.pixelSize: 11
                                                }
                                                MouseArea {
                                                    anchors.fill: parent
                                                    cursorShape: Qt.PointingHandCursor
                                                    onClicked: {
                                                        rt.setTextColor(sw.modelData === "" ? Qt.rgba(0,0,0,0) : sw.modelData)
                                                        colorPop.close()
                                                        body.forceActiveFocus()
                                                    }
                                                }
                                            }
                                        }
                                    }
                                }
                            }

                            ToolBtn {
                                symbol: "clear"
                                ToolTip.visible: hovered; ToolTip.delay: 400; ToolTip.text: "Clear formatting"
                                onClicked: { rt.clearFormatting(); body.forceActiveFocus() }
                            }
                        }

                        ToolSep {}

                        // Bold / Italic / Underline / Strike
                        Row {
                            spacing: 2
                            ToolBtn {
                                symbol: "bold"
                                active: rt.bold
                                ToolTip.visible: hovered; ToolTip.delay: 400; ToolTip.text: "Bold (Ctrl+B)"
                                onClicked: { rt.toggleBold(); body.forceActiveFocus() }
                            }
                            ToolBtn {
                                symbol: "italic"
                                active: rt.italic
                                ToolTip.visible: hovered; ToolTip.delay: 400; ToolTip.text: "Italic (Ctrl+I)"
                                onClicked: { rt.toggleItalic(); body.forceActiveFocus() }
                            }
                            ToolBtn {
                                symbol: "underline"
                                active: rt.underline
                                ToolTip.visible: hovered; ToolTip.delay: 400; ToolTip.text: "Underline (Ctrl+U)"
                                onClicked: { rt.toggleUnderline(); body.forceActiveFocus() }
                            }
                            ToolBtn {
                                symbol: "strike"
                                active: rt.strikeout
                                ToolTip.visible: hovered; ToolTip.delay: 400; ToolTip.text: "Strikethrough"
                                onClicked: { rt.toggleStrikeout(); body.forceActiveFocus() }
                            }
                        }

                        ToolSep {}

                        // Alignment
                        Row {
                            spacing: 2
                            ToolBtn {
                                symbol: "left"
                                active: rt.alignment === "left"
                                ToolTip.visible: hovered; ToolTip.delay: 400; ToolTip.text: "Align left"
                                onClicked: { rt.setAlignment("left"); body.forceActiveFocus() }
                            }
                            ToolBtn {
                                symbol: "center"
                                active: rt.alignment === "center"
                                ToolTip.visible: hovered; ToolTip.delay: 400; ToolTip.text: "Align center"
                                onClicked: { rt.setAlignment("center"); body.forceActiveFocus() }
                            }
                            ToolBtn {
                                symbol: "right"
                                active: rt.alignment === "right"
                                ToolTip.visible: hovered; ToolTip.delay: 400; ToolTip.text: "Align right"
                                onClicked: { rt.setAlignment("right"); body.forceActiveFocus() }
                            }
                        }

                        ToolSep {}

                        // Lists + Indent
                        Row {
                            spacing: 2
                            ToolBtn {
                                symbol: "bullets"
                                active: rt.listStyle === "bullet"
                                ToolTip.visible: hovered; ToolTip.delay: 400; ToolTip.text: "Bulleted list"
                                onClicked: { rt.toggleList("bullet"); body.forceActiveFocus() }
                            }
                            ToolBtn {
                                symbol: "numbers"
                                active: rt.listStyle === "numbered"
                                ToolTip.visible: hovered; ToolTip.delay: 400; ToolTip.text: "Numbered list"
                                onClicked: { rt.toggleList("numbered"); body.forceActiveFocus() }
                            }
                            ToolBtn {
                                symbol: "outdent"
                                ToolTip.visible: hovered; ToolTip.delay: 400; ToolTip.text: "Decrease indent"
                                onClicked: { rt.changeIndent(-1); body.forceActiveFocus() }
                            }
                            ToolBtn {
                                symbol: "indent"
                                ToolTip.visible: hovered; ToolTip.delay: 400; ToolTip.text: "Increase indent"
                                onClicked: { rt.changeIndent(1); body.forceActiveFocus() }
                            }
                        }
                    }
                }

                Rectangle {
                    Layout.fillWidth: true
                    Layout.preferredHeight: 1
                    color: Theme.border
                }

                ScrollView {
                    id: bodyScroll
                    Layout.fillWidth: true
                    Layout.fillHeight: true
                    clip: true
                    background: null
                    TextArea {
                        id: body
                        textFormat: TextEdit.RichText
                        onTextChanged: {
                            if (root.syncingBody)
                                return
                            const p = rt.plainText()
                            if (p !== c.body)
                                c.body = p
                        }
                        enabled: !root.busy
                        wrapMode: TextEdit.Wrap
                        color: Theme.text
                        selectionColor: Theme.selection
                        selectedTextColor: Theme.text
                        font.family: Theme.fontFamily
                        font.pixelSize: Theme.fontBody
                        padding: Theme.s3
                        background: null
                        KeyNavigation.priority: KeyNavigation.BeforeItem
                        KeyNavigation.tab: toField
                        KeyNavigation.backtab: subjectField
                    }
                }
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
                onClicked: root.sendNow()
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
