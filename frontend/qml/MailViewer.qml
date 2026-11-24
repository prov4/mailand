import QtQuick
import QtWebEngine
import Mailand

// Locked-down HTML mail viewer. Most HTML mail assumes a white canvas, so it sits in a light,
// rounded container.
Rectangle {
    id: root

    property alias view: web
    property url htmlUrl
    readonly property url source: web.url

    color: Theme.mailCanvas
    radius: Theme.radius
    clip: true

    function reload() { web.reload() }

    WebEngineView {
        id: web
        anchors.fill: parent
        profile: MailWeb.profile
        url: root.htmlUrl
        backgroundColor: Theme.mailCanvas
        focus: true

        settings.javascriptEnabled: false
        settings.localStorageEnabled: false
        settings.pluginsEnabled: false
        settings.autoLoadImages: true
        settings.javascriptCanOpenWindows: false
        settings.javascriptCanAccessClipboard: false
        settings.localContentCanAccessRemoteUrls: false
        settings.localContentCanAccessFileUrls: false
        settings.webGLEnabled: false

        onNavigationRequested: request => {
            // Only the main-frame load (or reload) of the current message is allowed.
            if (request.navigationType !== WebEngineNavigationRequest.LinkClickedNavigation
                    && request.isMainFrame
                    && request.url == root.htmlUrl) {
                request.accept()
                return
            }
            if (request.navigationType === WebEngineNavigationRequest.LinkClickedNavigation)
                MailWeb.openExternal(request.url)
            request.reject()
        }
        onNewWindowRequested: request => {
            MailWeb.openExternal(request.requestedUrl)
        }
    }
}
