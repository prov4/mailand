pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Mailand

// Comma-separated address field with contact autocomplete for the address
// being typed (the text between the previous separator and the cursor).
// Up/Down choose, Enter or Tab accept, Esc dismisses.
TextField {
    id: field

    readonly property bool suggesting: pop.visible
    property bool suggestionsWanted: false
    property int highlighted: 0

    // [start, end) of the address under the cursor.
    function tokenBounds(): var {
        const t = text
        let start = cursorPosition
        while (start > 0 && t[start - 1] !== "," && t[start - 1] !== ";")
            --start
        let end = cursorPosition
        while (end < t.length && t[end] !== "," && t[end] !== ";")
            ++end
        return [start, end]
    }
    readonly property string token: {
        cursorPosition; text // dependencies
        const b = tokenBounds()
        return text.substring(b[0], cursorPosition).trim()
    }

    function accept(row: int) {
        const item = suggestions.get(row)
        if (!item.address)
            return
        const b = tokenBounds()
        const before = text.substring(0, b[0]).replace(/\s*$/, "")
        const after = text.substring(b[1]).replace(/^[,;\s]*/, "")
        const head = before.length > 0 ? before + " " : ""
        const insert = item.address + ", "
        text = head + insert + after
        cursorPosition = (head + insert).length
        suggestionsWanted = false
        textEdited() // let the owner copy the new text
    }

    onTextEdited: {
        if (!suggestionsWanted)
            suggestionsWanted = true
        highlighted = 0
    }
    onActiveFocusChanged: if (!activeFocus) suggestionsWanted = false

    ContactsModel {
        id: suggestions
        perEmail: true
        limit: 8
        minQueryLength: 2
        query: field.suggestionsWanted ? field.token : ""
        onCountChanged: field.highlighted = Math.min(field.highlighted, Math.max(0, count - 1))
    }

    // Keep Esc (closes the composer) and Tab (focus chain) for the popup while it is showing.
    Keys.onShortcutOverride: event => {
        if (pop.visible && (event.key === Qt.Key_Escape || event.key === Qt.Key_Return || event.key === Qt.Key_Enter))
            event.accepted = true
    }
    Keys.priority: Keys.BeforeItem
    Keys.onPressed: event => {
        if (!pop.visible)
            return
        switch (event.key) {
        case Qt.Key_Down:
            field.highlighted = (field.highlighted + 1) % suggestions.count
            event.accepted = true
            break
        case Qt.Key_Up:
            field.highlighted = (field.highlighted + suggestions.count - 1) % suggestions.count
            event.accepted = true
            break
        case Qt.Key_Return:
        case Qt.Key_Enter:
        case Qt.Key_Tab:
            field.accept(field.highlighted)
            event.accepted = true
            break
        case Qt.Key_Escape:
            field.suggestionsWanted = false
            event.accepted = true
            break
        }
    }

    Popup {
        id: pop
        y: field.height + 2
        width: Math.max(field.width, 320)
        padding: 4
        visible: field.activeFocus && field.suggestionsWanted && suggestions.count > 0
        closePolicy: Popup.NoAutoClose
        focus: false
        background: Rectangle {
            color: Theme.surfaceAlt
            border.color: Theme.border
            radius: Theme.radius
        }
        contentItem: ListView {
            implicitHeight: contentHeight
            interactive: false
            model: suggestions
            delegate: Rectangle {
                id: row
                required property int index
                required property string fullName
                required property string email
                required property string initials
                required property string avatarColor
                width: ListView.view.width
                height: 40
                radius: Theme.radius
                color: row.index === field.highlighted ? Theme.selection : hover.hovered ? Theme.bg : "transparent"
                RowLayout {
                    anchors.fill: parent
                    anchors.leftMargin: Theme.s2
                    anchors.rightMargin: Theme.s2
                    spacing: Theme.s2
                    Rectangle {
                        Layout.preferredWidth: 26; Layout.preferredHeight: 26; radius: 13
                        color: row.avatarColor
                        Label { anchors.centerIn: parent; text: row.initials; color: "white"; font.pixelSize: 11; font.bold: true }
                    }
                    Column {
                        Layout.fillWidth: true
                        Label { width: parent.width; text: row.fullName; color: Theme.text; font.pixelSize: Theme.fontBody; elide: Text.ElideRight }
                        Label { width: parent.width; text: row.email; color: Theme.textMuted; font.pixelSize: Theme.fontSmall; elide: Text.ElideRight }
                    }
                }
                HoverHandler { id: hover }
                TapHandler { onTapped: field.accept(row.index) }
            }
        }
    }
}
