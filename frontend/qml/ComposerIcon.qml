import QtQuick
import QtQuick.Window

// Crisp vector SVG icons (Lucide-style 24x24 grid) for the composer toolbar.
Image {
    id: icon
    property string kind: ""
    property color ink: Theme.text
    property int iconSize: 18

    readonly property real dpr: Math.max(1.0, Screen.devicePixelRatio)

    width: iconSize
    height: iconSize
    // Rasterize the SVG at exact physical pixels (1:1) to avoid downscale blur.
    sourceSize: Qt.size(Math.round(iconSize * dpr), Math.round(iconSize * dpr))
    fillMode: Image.PreserveAspectFit
    smooth: false
    mipmap: false

    readonly property string strokeHex: Qt.color(ink).toString()

    function svgBody(k: string): string {
        switch (k) {
        case "chevron":
            return "<path d='M6 9l6 6 6-6'/>"
        case "bold":
            return "<path d='M6 4h8a4 4 0 0 1 0 8H6z'/><path d='M6 12h9a4 4 0 0 1 0 8H6z'/>"
        case "italic":
            return "<line x1='19' y1='4' x2='10' y2='4'/><line x1='14' y1='20' x2='5' y2='20'/><line x1='15' y1='4' x2='9' y2='20'/>"
        case "underline":
            return "<path d='M6 4v6a6 6 0 0 0 12 0V4'/><line x1='4' y1='20' x2='20' y2='20'/>"
        case "strike":
            return "<path d='M16 4H9a3 3 0 0 0-2.83 4'/><path d='M14 12a4 4 0 0 1 0 8H6'/><line x1='4' y1='12' x2='20' y2='12'/>"
        case "clear":
            return "<path d='M4 7V4h16v3'/><path d='M9 20h6'/><path d='M12 4v16'/><line x1='3' y1='21' x2='21' y2='3'/>"
        case "left":
            return "<line x1='3' y1='6' x2='21' y2='6'/><line x1='3' y1='12' x2='15' y2='12'/><line x1='3' y1='18' x2='18' y2='18'/>"
        case "center":
            return "<line x1='3' y1='6' x2='21' y2='6'/><line x1='6' y1='12' x2='18' y2='12'/><line x1='4' y1='18' x2='20' y2='18'/>"
        case "right":
            return "<line x1='3' y1='6' x2='21' y2='6'/><line x1='9' y1='12' x2='21' y2='12'/><line x1='6' y1='18' x2='21' y2='18'/>"
        case "bullets":
            return "<circle cx='4.5' cy='6' r='1.2' fill='currentColor' stroke='none'/>"
                 + "<circle cx='4.5' cy='12' r='1.2' fill='currentColor' stroke='none'/>"
                 + "<circle cx='4.5' cy='18' r='1.2' fill='currentColor' stroke='none'/>"
                 + "<line x1='9' y1='6' x2='21' y2='6'/><line x1='9' y1='12' x2='21' y2='12'/><line x1='9' y1='18' x2='21' y2='18'/>"
        case "numbers":
            return "<path d='M4 5h1v4'/><path d='M3.5 9h2'/>"
                 + "<path d='M3.5 14a1.5 1.5 0 0 1 2.5 1c0 .8-1 1.2-2.5 2.5h3'/>"
                 + "<line x1='10' y1='6' x2='21' y2='6'/><line x1='10' y1='12' x2='21' y2='12'/><line x1='10' y1='18' x2='21' y2='18'/>"
        case "outdent":
            return "<polyline points='7 8 3 12 7 16'/>"
                 + "<line x1='11' y1='6' x2='21' y2='6'/><line x1='11' y1='12' x2='21' y2='12'/><line x1='11' y1='18' x2='21' y2='18'/>"
        case "indent":
            return "<polyline points='3 8 7 12 3 16'/>"
                 + "<line x1='11' y1='6' x2='21' y2='6'/><line x1='11' y1='12' x2='21' y2='12'/><line x1='11' y1='18' x2='21' y2='18'/>"
        default:
            return ""
        }
    }

    source: {
        const body = svgBody(kind)
        if (!body)
            return ""
        const col = strokeHex.replace("#", "%23")
        const svg = "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='none' color='"
                  + col + "' stroke='" + col + "' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'>"
                  + body + "</svg>"
        return "data:image/svg+xml;utf8," + svg
    }
}
