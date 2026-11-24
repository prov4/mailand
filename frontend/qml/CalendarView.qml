pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Mailand

// Month grid (Monday first) with event chips, and the selected day's agenda.
Rectangle {
    id: root
    required property CalendarModel calendar
    color: Theme.bg

    signal eventActivated(string eventId)

    // Enter: open the selected day's first event.
    function openFirst() {
        if (selectedEvents.length > 0)
            eventActivated(selectedEvents[0].id)
    }
    function pending(ev: var): bool { return ev.canRespond && (ev.myStatus === "NEEDS-ACTION" || ev.myStatus === "") }
    function declined(ev: var): bool { return ev.myStatus === "DECLINED" }

    readonly property var selectedEvents: calendar.eventsOn(calendar.selectedDate, calendar.revision)

    component NavButton: Button {
        id: nb
        flat: true
        focusPolicy: Qt.NoFocus
        implicitHeight: 30
        implicitWidth: Math.max(30, contentItem.implicitWidth + 2 * Theme.s3)
        background: Rectangle {
            radius: Theme.radius
            color: nb.down ? Theme.selection : nb.hovered ? Theme.surfaceAlt : "transparent"
            border.color: Theme.border
        }
        contentItem: Label {
            text: nb.text
            color: Theme.text
            font.pixelSize: Theme.fontSmall
            horizontalAlignment: Text.AlignHCenter
            verticalAlignment: Text.AlignVCenter
        }
    }

    RowLayout {
        anchors.fill: parent
        spacing: 0

        // ---- Month -------------------------------------------------------------------------
        ColumnLayout {
            Layout.fillWidth: true
            Layout.fillHeight: true
            Layout.margins: Theme.s3
            spacing: Theme.s2

            RowLayout {
                Layout.fillWidth: true
                spacing: Theme.s2
                Label {
                    text: root.calendar.monthTitle
                    color: Theme.text
                    font.pixelSize: Theme.fontTitle + 2
                    font.bold: true
                    Layout.fillWidth: true
                }
                BusyIndicator {
                    Layout.preferredWidth: 18; Layout.preferredHeight: 18
                    running: visible
                    visible: root.calendar.loading
                }
                NavButton { text: "‹"; onClicked: root.calendar.moveMonths(-1); ToolTip.text: "Previous month (p)"; ToolTip.visible: hovered; ToolTip.delay: 400 }
                NavButton { text: "Today"; onClicked: root.calendar.goToday(); ToolTip.text: "Go to today (t)"; ToolTip.visible: hovered; ToolTip.delay: 400 }
                NavButton { text: "›"; onClicked: root.calendar.moveMonths(1); ToolTip.text: "Next month (n)"; ToolTip.visible: hovered; ToolTip.delay: 400 }
            }

            // Weekday header
            RowLayout {
                Layout.fillWidth: true
                spacing: 0
                Repeater {
                    model: 7
                    delegate: Label {
                        required property int index
                        Layout.fillWidth: true
                        Layout.preferredWidth: 1
                        text: Qt.locale().dayName((index + 1) % 7, Locale.ShortFormat)
                        color: Theme.textFaint
                        font.pixelSize: Theme.fontSmall
                        horizontalAlignment: Text.AlignHCenter
                    }
                }
            }

            GridLayout {
                id: grid
                Layout.fillWidth: true
                Layout.fillHeight: true
                columns: 7
                rows: 6
                columnSpacing: 1
                rowSpacing: 1

                Repeater {
                    model: 42
                    delegate: Rectangle {
                        id: cell
                        required property int index
                        readonly property date day: root.calendar.gridDate(index, root.calendar.month)
                        readonly property bool inMonth: day.getMonth() === root.calendar.month.getMonth()
                        readonly property bool isToday: {
                            const t = new Date()
                            return day.getFullYear() === t.getFullYear() && day.getMonth() === t.getMonth() && day.getDate() === t.getDate()
                        }
                        readonly property bool isSelected: {
                            const s = root.calendar.selectedDate
                            return day.getFullYear() === s.getFullYear() && day.getMonth() === s.getMonth() && day.getDate() === s.getDate()
                        }
                        readonly property var events: root.calendar.eventsOn(day, root.calendar.revision)
                        readonly property int capacity: Math.max(0, Math.floor((height - 26) / 18))
                        readonly property int shown: events.length > capacity ? Math.max(0, capacity - 1) : events.length

                        Layout.fillWidth: true
                        Layout.fillHeight: true
                        Layout.preferredWidth: 1
                        Layout.preferredHeight: 1
                        radius: 4
                        color: isSelected ? Theme.selection : inMonth ? Theme.surface : Theme.bg
                        border.color: isSelected ? Theme.accent : "transparent"
                        clip: true

                        Rectangle {
                            x: 6; y: 4
                            width: 22; height: 20; radius: 10
                            color: cell.isToday ? Theme.accent : "transparent"
                            Label {
                                anchors.centerIn: parent
                                text: cell.day.getDate()
                                color: cell.isToday ? Theme.accentText : cell.inMonth ? Theme.text : Theme.textFaint
                                font.pixelSize: Theme.fontSmall
                                font.bold: cell.isToday
                            }
                        }

                        Column {
                            x: 4; y: 26
                            width: cell.width - 8
                            spacing: 2
                            Repeater {
                                model: cell.events.slice(0, cell.shown)
                                delegate: Rectangle {
                                    id: chip
                                    required property var modelData
                                    readonly property bool bar: chip.modelData.allDay || chip.modelData.multiDay
                                    readonly property bool pending: root.pending(chip.modelData)
                                    width: parent.width
                                    height: 16
                                    radius: 3
                                    opacity: root.declined(chip.modelData) ? 0.45 : 1
                                    color: chip.bar && !chip.pending ? Qt.alpha(chip.modelData.color, 0.85)
                                         : chipHover.hovered ? Theme.surfaceAlt : "transparent"
                                    // Unanswered invitations are outlined.
                                    border.color: chip.pending ? chip.modelData.color : "transparent"
                                    border.width: 1
                                    Row {
                                        anchors.fill: parent
                                        anchors.leftMargin: 4
                                        spacing: 4
                                        Rectangle {
                                            visible: !chip.bar
                                            width: 6; height: 6; radius: 3
                                            anchors.verticalCenter: parent.verticalCenter
                                            color: chip.modelData.color
                                        }
                                        Label {
                                            width: parent.width - 14
                                            anchors.verticalCenter: parent.verticalCenter
                                            text: (chip.bar ? "" : chip.modelData.startText + " ") + chip.modelData.summary
                                            color: chip.bar && !chip.pending ? Theme.accentText : cell.inMonth ? Theme.text : Theme.textMuted
                                            font.pixelSize: 11
                                            font.strikeout: root.declined(chip.modelData)
                                            elide: Text.ElideRight
                                        }
                                    }
                                    HoverHandler { id: chipHover; cursorShape: Qt.PointingHandCursor }
                                    TapHandler {
                                        onTapped: {
                                            root.calendar.select(cell.day)
                                            root.eventActivated(chip.modelData.id)
                                        }
                                    }
                                }
                            }
                            Label {
                                visible: cell.events.length > cell.shown
                                text: "+" + (cell.events.length - cell.shown) + " more"
                                color: Theme.textMuted
                                font.pixelSize: 11
                                leftPadding: 4
                            }
                        }
                        TapHandler { onTapped: root.calendar.select(cell.day) }
                    }
                }
            }
        }

        Rectangle { Layout.fillHeight: true; Layout.preferredWidth: 1; color: Theme.border }

        // ---- Agenda for the selected day ----------------------------------------------------
        ColumnLayout {
            Layout.preferredWidth: 320
            Layout.minimumWidth: 260
            Layout.maximumWidth: 360
            Layout.fillWidth: false
            Layout.fillHeight: true
            Layout.margins: Theme.s4
            spacing: Theme.s3

            Label {
                text: Qt.locale().toString(root.calendar.selectedDate, "dddd")
                color: Theme.textMuted
                font.pixelSize: Theme.fontSmall
            }
            Label {
                Layout.topMargin: -Theme.s3
                text: Qt.locale().toString(root.calendar.selectedDate, "d MMMM yyyy")
                color: Theme.text
                font.pixelSize: Theme.fontTitle
                font.bold: true
            }

            ListView {
                id: agenda
                Layout.fillWidth: true
                Layout.fillHeight: true
                clip: true
                spacing: Theme.s2
                model: root.selectedEvents
                boundsBehavior: Flickable.StopAtBounds
                ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

                delegate: Rectangle {
                    id: card
                    required property var modelData
                    width: ListView.view.width
                    height: body.implicitHeight + 2 * Theme.s3
                    radius: Theme.radius
                    color: cardHover.hovered ? Theme.surfaceAlt : Theme.surface
                    border.color: root.pending(card.modelData) ? card.modelData.color : Theme.border
                    opacity: root.declined(card.modelData) ? 0.6 : 1
                    HoverHandler { id: cardHover; cursorShape: Qt.PointingHandCursor }
                    TapHandler { onTapped: root.eventActivated(card.modelData.id) }

                    Rectangle {
                        width: 4
                        height: parent.height - 2 * Theme.s2
                        anchors.left: parent.left
                        anchors.leftMargin: Theme.s2
                        anchors.verticalCenter: parent.verticalCenter
                        radius: 2
                        color: card.modelData.color
                    }
                    ColumnLayout {
                        id: body
                        anchors { left: parent.left; right: parent.right; top: parent.top; margins: Theme.s3; leftMargin: Theme.s5 }
                        spacing: 2
                        RowLayout {
                            Layout.fillWidth: true
                            Label {
                                Layout.fillWidth: true
                                text: card.modelData.summary
                                color: Theme.text
                                font.pixelSize: Theme.fontBody
                                font.bold: true
                                font.strikeout: root.declined(card.modelData)
                                wrapMode: Text.Wrap
                            }
                            Rectangle {
                                visible: badge.text.length > 0
                                Layout.preferredHeight: 18
                                Layout.preferredWidth: badge.implicitWidth + 12
                                radius: 9
                                color: root.pending(card.modelData) ? Theme.accent : Theme.surfaceAlt
                                Label {
                                    id: badge
                                    anchors.centerIn: parent
                                    text: root.pending(card.modelData) ? "Invitation"
                                        : card.modelData.myStatus === "ACCEPTED" && card.modelData.canRespond ? "Going"
                                        : card.modelData.myStatus === "TENTATIVE" ? "Maybe"
                                        : card.modelData.myStatus === "DECLINED" ? "Declined" : ""
                                    color: root.pending(card.modelData) ? Theme.accentText : Theme.textMuted
                                    font.pixelSize: 10
                                    font.bold: true
                                }
                            }
                        }
                        Label {
                            Layout.fillWidth: true
                            text: card.modelData.timeText
                                  + (card.modelData.recurring ? "  ·  ↻ repeats" : "")
                                  + (card.modelData.status === "TENTATIVE" ? "  ·  tentative" : "")
                            color: Theme.textMuted
                            font.pixelSize: Theme.fontSmall
                            wrapMode: Text.Wrap
                        }
                        Label {
                            Layout.fillWidth: true
                            visible: text.length > 0
                            text: card.modelData.location
                            color: Theme.textMuted
                            font.pixelSize: Theme.fontSmall
                            wrapMode: Text.Wrap
                        }
                        Label {
                            Layout.fillWidth: true
                            visible: text.length > 0
                            text: card.modelData.organizer.length > 0 ? "Organizer: " + card.modelData.organizer : ""
                            color: Theme.textFaint
                            font.pixelSize: Theme.fontSmall
                            elide: Text.ElideRight
                        }
                        Label {
                            Layout.fillWidth: true
                            Layout.topMargin: Theme.s1
                            visible: text.length > 0
                            text: card.modelData.description
                            color: Theme.text
                            font.pixelSize: Theme.fontSmall
                            wrapMode: Text.Wrap
                            maximumLineCount: 8
                            elide: Text.ElideRight
                            textFormat: Text.PlainText
                        }
                        Label {
                            Layout.fillWidth: true
                            text: card.modelData.calendarName
                            color: Theme.textFaint
                            font.pixelSize: 11
                        }
                    }
                }

                Label {
                    anchors.centerIn: parent
                    visible: agenda.count === 0
                    text: root.calendar.calendars.length === 0
                          ? "No calendars synced yet.\nSee the README for setup."
                          : "No events"
                    horizontalAlignment: Text.AlignHCenter
                    color: Theme.textFaint
                    font.pixelSize: Theme.fontBody
                }
            }
        }
    }
}
