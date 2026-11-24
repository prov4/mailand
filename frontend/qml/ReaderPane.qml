pragma ComponentBehavior: Bound
import QtCore
import QtQuick
import QtQuick.Controls
import QtQuick.Dialogs
import QtQuick.Layouts
import Mailand

FocusScope {
    id: root

    required property MessageView message
    signal addSenderToContacts(string name, string email)
    property bool remoteBlocked: false
    readonly property bool hasMessage: root.message.messageId.length > 0

    function focusContent() {
        if (message.hasHtml)
            viewer.view.forceActiveFocus()
        else
            body.forceActiveFocus()
    }

    Connections {
        target: root.message
        function onMessageIdChanged() { root.remoteBlocked = false }
    }
    Connections {
        target: MailWeb
        function onRemoteContentBlocked() { root.remoteBlocked = true }
    }

    Rectangle { anchors.fill: parent; color: Theme.bg }

    // Empty state
    Text {
        anchors.centerIn: parent
        visible: !root.hasMessage
        text: "Select a message to read"
        color: Theme.textFaint
        font.pixelSize: Theme.fontBody
    }

    // Loading state
    BusyIndicator {
        anchors.centerIn: parent
        running: visible
        visible: root.hasMessage && root.message.loading && !root.message.valid
    }

    // Error state
    Text {
        anchors.centerIn: parent
        width: parent.width - 2 * Theme.s5
        horizontalAlignment: Text.AlignHCenter
        wrapMode: Text.Wrap
        visible: root.hasMessage && !root.message.loading && !root.message.valid
        text: root.message.errorText.length > 0 ? root.message.errorText : "Could not load message"
        color: Theme.danger
        font.pixelSize: Theme.fontBody
    }

    ColumnLayout {
        anchors.fill: parent
        spacing: 0
        visible: root.hasMessage && root.message.valid

        // Header
        ColumnLayout {
            Layout.fillWidth: true
            Layout.margins: Theme.s4
            spacing: Theme.s1

            Text {
                Layout.fillWidth: true
                text: root.message.subject
                color: Theme.text
                font.pixelSize: Theme.fontTitle
                font.bold: true
                wrapMode: Text.Wrap
                maximumLineCount: 3
                elide: Text.ElideRight
            }
            RowLayout {
                Layout.fillWidth: true
                spacing: Theme.s2
                Text {
                    Layout.fillWidth: true
                    text: root.message.fromName.length > 0
                          ? root.message.fromName + " <" + root.message.fromAddress + ">"
                          : root.message.fromAddress
                    color: Theme.text
                    font.pixelSize: Theme.fontBody
                    elide: Text.ElideRight
                }
                Text {
                    id: addSender
                    text: "+ Contact"
                    color: addHover.hovered ? Theme.accent : Theme.textFaint
                    font.pixelSize: Theme.fontSmall
                    visible: root.message.fromAddress.length > 0
                    HoverHandler { id: addHover; cursorShape: Qt.PointingHandCursor }
                    TapHandler { onTapped: root.addSenderToContacts(root.message.fromName, root.message.fromAddress) }
                    ToolTip.visible: addHover.hovered
                    ToolTip.text: "Add the sender to your contacts"
                    ToolTip.delay: 400
                }
                Text {
                    text: root.message.dateText
                    color: Theme.textMuted
                    font.pixelSize: Theme.fontSmall
                }
            }
            Text {
                Layout.fillWidth: true
                visible: root.message.to.length > 0
                text: "To: " + root.message.to.join(", ")
                color: Theme.textMuted
                font.pixelSize: Theme.fontSmall
                elide: Text.ElideRight
            }
            Text {
                Layout.fillWidth: true
                visible: root.message.cc.length > 0
                text: "Cc: " + root.message.cc.join(", ")
                color: Theme.textMuted
                font.pixelSize: Theme.fontSmall
                elide: Text.ElideRight
            }

            Flow {
                Layout.fillWidth: true
                Layout.topMargin: Theme.s1
                spacing: Theme.s2
                visible: root.message.attachments.length > 0
                Repeater {
                    model: root.message.attachments
                    delegate: Rectangle {
                        id: chip
                        required property var modelData
                        implicitWidth: chipRow.implicitWidth + 2 * Theme.s3
                        implicitHeight: 28
                        radius: 14
                        color: chipArea.containsMouse ? Theme.surfaceAlt : Theme.surface
                        border.color: Theme.border
                        Row {
                            id: chipRow
                            anchors.centerIn: parent
                            spacing: Theme.s1
                            IconGlyph { name: "clip"; font.pixelSize: Theme.fontSmall }
                            Text {
                                text: chip.modelData.filename
                                color: Theme.text
                                font.pixelSize: Theme.fontSmall
                            }
                            Text {
                                text: chip.modelData.sizeText
                                color: Theme.textMuted
                                font.pixelSize: Theme.fontSmall
                            }
                        }
                        MouseArea {
                            id: chipArea
                            anchors.fill: parent
                            hoverEnabled: true
                            cursorShape: Qt.PointingHandCursor
                            onClicked: {
                                saveDialog.attachmentId = chip.modelData.id
                                saveDialog.currentFolder = StandardPaths.writableLocation(StandardPaths.DownloadLocation)
                                saveDialog.currentFile = saveDialog.currentFolder + "/" + chip.modelData.filename
                                saveDialog.open()
                            }
                        }
                    }
                }
            }
        }

        // Remote images banner
        Rectangle {
            Layout.fillWidth: true
            Layout.preferredHeight: 36
            visible: root.remoteBlocked && !MailWeb.remoteAllowed && root.message.hasHtml
            color: Theme.surfaceAlt
            RowLayout {
                anchors.fill: parent
                anchors.leftMargin: Theme.s4
                anchors.rightMargin: Theme.s4
                Text {
                    Layout.fillWidth: true
                    text: "Remote images were blocked to protect your privacy."
                    color: Theme.textMuted
                    font.pixelSize: Theme.fontSmall
                    elide: Text.ElideRight
                }
                Button {
                    id: loadBtn
                    text: "Load remote images"
                    implicitHeight: 26
                    onClicked: {
                        MailWeb.remoteAllowed = true
                        root.remoteBlocked = false
                        viewer.reload()
                    }
                    background: Rectangle { radius: Theme.radius; color: loadBtn.hovered ? Theme.selection : Theme.surface; border.color: Theme.border }
                    contentItem: Text {
                        text: loadBtn.text
                        color: Theme.text
                        font.pixelSize: Theme.fontSmall
                        horizontalAlignment: Text.AlignHCenter
                        verticalAlignment: Text.AlignVCenter
                    }
                }
            }
        }
        Rectangle { Layout.fillWidth: true; Layout.preferredHeight: 1; color: Theme.border }

        // Body
        Item {
            Layout.fillWidth: true
            Layout.fillHeight: true

            MailViewer {
                id: viewer
                htmlUrl: root.message.htmlUrl
                anchors.fill: parent
                anchors.margins: Theme.s2
                visible: root.message.hasHtml
            }

            ScrollView {
                anchors.fill: parent
                anchors.margins: Theme.s2
                visible: !root.message.hasHtml
                TextArea {
                    id: body
                    text: root.message.textBody
                    readOnly: true
                    selectByMouse: true
                    wrapMode: TextEdit.Wrap
                    color: Theme.text
                    selectionColor: Theme.accent
                    selectedTextColor: Theme.accentText
                    font.family: Theme.monoFamily
                    font.pixelSize: Theme.fontBody
                    padding: Theme.s3
                    background: null
                }
            }
        }
    }

    FileDialog {
        id: saveDialog
        property string attachmentId
        fileMode: FileDialog.SaveFile
        onAccepted: root.message.saveAttachment(attachmentId, selectedFile)
    }
}
