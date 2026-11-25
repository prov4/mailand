import QtQuick
import QtQuick.Controls

// Clean, flat, theme-aware dropdown for the composer toolbar and header.
ComboBox {
    id: control
    implicitWidth: 112
    implicitHeight: 30
    focusPolicy: Qt.NoFocus
    font.pixelSize: 13
    leftPadding: 10
    rightPadding: 24

    contentItem: Text {
        text: control.displayText
        font: control.font
        color: control.enabled ? Theme.text : Theme.textFaint
        verticalAlignment: Text.AlignVCenter
        elide: Text.ElideRight
    }

    indicator: ComposerIcon {
        x: control.width - width - 6
        y: (control.height - height) / 2
        kind: "chevron"
        ink: Theme.textMuted
        iconSize: 14
    }

    background: Rectangle {
        radius: Theme.radius
        color: control.popup.visible
               ? Qt.alpha(Theme.accent, 0.12)
               : (control.hovered ? Qt.alpha(Theme.text, 0.07) : "transparent")
        border.color: control.popup.visible ? Qt.alpha(Theme.accent, 0.4) : "transparent"
    }

    delegate: ItemDelegate {
        id: del
        required property int index
        required property var modelData
        width: ListView.view ? ListView.view.width : control.width
        height: 32
        highlighted: control.highlightedIndex === index
        contentItem: Text {
            text: del.modelData
            color: del.highlighted ? Theme.accent : Theme.text
            font.pixelSize: 13
            font.bold: control.currentIndex === del.index
            verticalAlignment: Text.AlignVCenter
            elide: Text.ElideRight
            leftPadding: 6
            rightPadding: 6
        }
        background: Rectangle {
            radius: Theme.radius - 1
            color: del.highlighted ? Qt.alpha(Theme.accent, 0.14) : "transparent"
        }
    }

    popup: Popup {
        y: control.height + 4
        width: Math.max(control.width, 136)
        padding: 4
        implicitHeight: contentItem.implicitHeight + 8
        background: Rectangle {
            color: Theme.surface
            border.color: Theme.border
            radius: Theme.radius
        }
        contentItem: ListView {
            clip: true
            implicitHeight: contentHeight
            model: control.popup.visible ? control.delegateModel : null
            currentIndex: control.highlightedIndex
            ScrollIndicator.vertical: ScrollIndicator {}
        }
    }
}
