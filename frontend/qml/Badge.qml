import QtQuick

Rectangle {
    id: root
    property int count: 0
    visible: count > 0
    implicitWidth: Math.max(implicitHeight, label.implicitWidth + 10)
    implicitHeight: 18
    radius: height / 2
    color: Theme.accent

    Text {
        id: label
        anchors.centerIn: parent
        text: root.count > 999 ? "999+" : root.count
        color: Theme.accentText
        font.pixelSize: 11
        font.bold: true
    }
}
