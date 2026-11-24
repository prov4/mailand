pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Mailand

// Details of one event occurrence; for invitations, Yes / Maybe / No.
// Keys: y / m / n respond, Esc closes.
Popup {
    id: root
    required property CalendarModel calendar
    property string eventId
    readonly property var ev: calendar.eventById(eventId, calendar.revision)
    readonly property bool valid: ev.id !== undefined
    readonly property bool invited: valid && ev.canRespond

    function show(id: string) {
        eventId = id
        open()
    }
    function respond(status: string) {
        if (invited && ev.myStatus !== status)
            calendar.respond(eventId, status)
    }

    modal: true
    focus: true
    anchors.centerIn: Overlay.overlay
    width: Math.min(560, (parent ? parent.width : 560) - 2 * Theme.s5)
    height: Math.min(content.implicitHeight + 2 * padding, (parent ? parent.height : 700) - 2 * Theme.s5)
    padding: Theme.s5
    closePolicy: Popup.CloseOnEscape | Popup.CloseOnPressOutside
    onClosed: eventId = ""

    background: Rectangle {
        color: Theme.surface
        radius: Theme.radiusLarge
        border.color: Theme.border
    }
    Overlay.modal: Rectangle { color: "#99000000" }

    Shortcut { sequence: "y"; enabled: root.opened && root.invited; onActivated: root.respond("ACCEPTED") }
    Shortcut { sequence: "m"; enabled: root.opened && root.invited; onActivated: root.respond("TENTATIVE") }
    Shortcut { sequence: "n"; enabled: root.opened && root.invited; onActivated: root.respond("DECLINED") }

    function statusText(st: string): string {
        switch (st) {
        case "ACCEPTED": return "Going"
        case "TENTATIVE": return "Maybe"
        case "DECLINED": return "Not going"
        case "DELEGATED": return "Delegated"
        default: return "Not answered"
        }
    }
    function statusGlyph(st: string): string {
        switch (st) {
        case "ACCEPTED": return "✓"
        case "TENTATIVE": return "?"
        case "DECLINED": return "✕"
        default: return "·"
        }
    }
    function statusColor(st: string): color {
        switch (st) {
        case "ACCEPTED": return Theme.success
        case "TENTATIVE": return Theme.flag
        case "DECLINED": return Theme.danger
        default: return Theme.textFaint
        }
    }

    component RsvpButton: Button {
        id: rb
        required property string value
        property string label
        readonly property bool chosen: root.ev.myStatus === value
        Layout.fillWidth: true
        Layout.preferredWidth: 1 // equal widths
        implicitHeight: 34
        focusPolicy: Qt.NoFocus
        enabled: root.invited
        onClicked: root.respond(value)
        background: Rectangle {
            radius: Theme.radius
            color: rb.chosen ? root.statusColor(rb.value) : rb.hovered ? Theme.surfaceAlt : Theme.bg
            border.color: rb.chosen ? root.statusColor(rb.value) : Theme.border
        }
        contentItem: Label {
            text: rb.label
            color: rb.chosen ? Theme.accentText : Theme.text
            font.pixelSize: Theme.fontBody
            font.bold: rb.chosen
            horizontalAlignment: Text.AlignHCenter
            verticalAlignment: Text.AlignVCenter
        }
    }

    contentItem: Flickable {
        id: flick
        clip: true
        contentHeight: content.implicitHeight
        boundsBehavior: Flickable.StopAtBounds
        ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

        ColumnLayout {
            id: content
            width: flick.width
            spacing: Theme.s3

            RowLayout {
                Layout.fillWidth: true
                spacing: Theme.s3
                Rectangle {
                    Layout.preferredWidth: 6
                    Layout.fillHeight: true
                    radius: 3
                    color: root.valid ? root.ev.color : Theme.border
                }
                ColumnLayout {
                    Layout.fillWidth: true
                    spacing: 2
                    Label {
                        Layout.fillWidth: true
                        text: root.valid ? root.ev.summary : "Event no longer exists"
                        color: Theme.text
                        font.pixelSize: Theme.fontTitle + 2
                        font.bold: true
                        wrapMode: Text.Wrap
                    }
                    Label {
                        Layout.fillWidth: true
                        visible: root.valid
                        text: root.valid ? root.ev.dateText + "  ·  " + root.ev.timeText
                                           + (root.ev.recurring ? "  ·  ↻ repeats" : "") : ""
                        color: Theme.textMuted
                        font.pixelSize: Theme.fontBody
                        wrapMode: Text.Wrap
                    }
                    Label {
                        visible: root.valid
                        text: root.valid ? root.ev.calendarName : ""
                        color: Theme.textFaint
                        font.pixelSize: Theme.fontSmall
                    }
                }
            }

            // RSVP
            ColumnLayout {
                Layout.fillWidth: true
                visible: root.invited
                spacing: Theme.s2
                Label {
                    text: root.ev.myStatus === "NEEDS-ACTION" || root.ev.myStatus === ""
                          ? "You're invited. Going?" : "Your answer: " + root.statusText(root.ev.myStatus)
                    color: Theme.text
                    font.pixelSize: Theme.fontBody
                    font.bold: true
                }
                RowLayout {
                    Layout.fillWidth: true
                    spacing: Theme.s2
                    RsvpButton { value: "ACCEPTED"; label: "Yes  (y)" }
                    RsvpButton { value: "TENTATIVE"; label: "Maybe  (m)" }
                    RsvpButton { value: "DECLINED"; label: "No  (n)" }
                }
                Label {
                    Layout.fillWidth: true
                    visible: root.valid && root.ev.recurring
                    text: "Your answer applies to every occurrence of this event."
                    color: Theme.textFaint
                    font.pixelSize: Theme.fontSmall
                    wrapMode: Text.Wrap
                }
            }

            GridLayout {
                Layout.fillWidth: true
                visible: root.valid
                columns: 2
                columnSpacing: Theme.s3
                rowSpacing: Theme.s1
                Label { visible: (root.ev.location ?? "").length > 0; text: "Where"; color: Theme.textFaint; font.pixelSize: Theme.fontSmall }
                TextEdit {
                    Layout.fillWidth: true
                    visible: (root.ev.location ?? "").length > 0
                    text: root.ev.location ?? ""
                    readOnly: true
                    selectByMouse: true
                    wrapMode: TextEdit.Wrap
                    color: Theme.text
                    selectionColor: Theme.selection
                    font.pixelSize: Theme.fontBody
                }
                Label { visible: (root.ev.organizer ?? "").length > 0; text: "Organizer"; color: Theme.textFaint; font.pixelSize: Theme.fontSmall }
                Label {
                    Layout.fillWidth: true
                    visible: (root.ev.organizer ?? "").length > 0
                    text: (root.ev.organizer ?? "")
                          + ((root.ev.organizerEmail ?? "").length > 0 && root.ev.organizer !== root.ev.organizerEmail
                             ? "  <" + root.ev.organizerEmail + ">" : "")
                    color: Theme.text
                    font.pixelSize: Theme.fontBody
                    elide: Text.ElideRight
                }
            }

            // Attendees
            ColumnLayout {
                Layout.fillWidth: true
                visible: (root.ev.attendees ?? []).length > 0
                spacing: 4
                Label {
                    text: {
                        const a = root.ev.attendees ?? []
                        let yes = 0, maybe = 0, no = 0, open = 0
                        for (const x of a) {
                            if (x.status === "ACCEPTED") ++yes
                            else if (x.status === "TENTATIVE") ++maybe
                            else if (x.status === "DECLINED") ++no
                            else ++open
                        }
                        return a.length + " guests  ·  " + yes + " yes, " + maybe + " maybe, " + no + " no, " + open + " awaiting"
                    }
                    color: Theme.textFaint
                    font.pixelSize: Theme.fontSmall
                }
                Repeater {
                    model: root.ev.attendees ?? []
                    delegate: RowLayout {
                        id: guest
                        required property var modelData
                        Layout.fillWidth: true
                        spacing: Theme.s2
                        Label {
                            Layout.preferredWidth: 16
                            text: root.statusGlyph(guest.modelData.status)
                            color: root.statusColor(guest.modelData.status)
                            font.pixelSize: Theme.fontBody
                            font.bold: true
                            horizontalAlignment: Text.AlignHCenter
                        }
                        Label {
                            Layout.fillWidth: true
                            text: (guest.modelData.name.length > 0 ? guest.modelData.name : guest.modelData.email)
                                  + (guest.modelData.isMe ? "  (you)" : "")
                                  + (guest.modelData.role === "CHAIR" || guest.modelData.email === root.ev.organizerEmail ? "  · organizer" : "")
                                  + (guest.modelData.role === "OPT-PARTICIPANT" ? "  · optional" : "")
                            color: Theme.text
                            font.pixelSize: Theme.fontBody
                            font.bold: guest.modelData.isMe
                            elide: Text.ElideRight
                        }
                        Label {
                            text: root.statusText(guest.modelData.status)
                            color: Theme.textMuted
                            font.pixelSize: Theme.fontSmall
                        }
                    }
                }
            }

            TextEdit {
                Layout.fillWidth: true
                Layout.topMargin: Theme.s2
                visible: text.length > 0
                text: root.ev.description ?? ""
                readOnly: true
                selectByMouse: true
                wrapMode: TextEdit.Wrap
                textFormat: TextEdit.PlainText
                color: Theme.text
                selectionColor: Theme.selection
                font.pixelSize: Theme.fontBody
            }
        }
    }
}
