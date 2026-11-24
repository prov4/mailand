#include <QCoreApplication>
#include <QGuiApplication>
#include <QImage>
#include <QQmlApplicationEngine>
#include <QQuickWindow>
#include <QTextStream>
#include <QTimer>
#include <QtQuickControls2/QQuickStyle>
#include <QtWebEngineQuick/qtwebenginequickglobal.h>

#include <cstring>

#include "MailWeb.h"
#include "Rpc.h"
#include "SelfTest.h"

static bool hasArg(int argc, char **argv, const char *flag)
{
    for (int i = 1; i < argc; ++i)
        if (std::strcmp(argv[i], flag) == 0)
            return true;
    return false;
}

static QString argValue(int argc, char **argv, const char *flag)
{
    for (int i = 1; i + 1 < argc; ++i)
        if (std::strcmp(argv[i], flag) == 0)
            return QString::fromLocal8Bit(argv[i + 1]);
    return {};
}

// Headless: no display needed, so it uses QCoreApplication only.
static int runPrint(int argc, char **argv)
{
    QCoreApplication app(argc, argv);
    QList<AccountInfo> accounts;
    QString error;
    if (!Rpc::listAccountsSync(&accounts, &error)) {
        QTextStream(stderr) << "mailand-ui: " << error << Qt::endl;
        return 1;
    }
    QTextStream out(stdout);
    for (const auto &a : accounts)
        out << a.id << '\t' << a.name << '\t' << a.email << '\n';
    return 0;
}

int main(int argc, char *argv[])
{
    if (hasArg(argc, argv, "--print"))
        return runPrint(argc, argv);
    if (hasArg(argc, argv, "--selftest"))
        return runSelfTest(argc, argv);

    if (qEnvironmentVariableIsEmpty("QT_QPA_PLATFORM"))
        qputenv("QT_QPA_PLATFORM", "wayland");

    // Order matters: WebEngine init and custom schemes must precede the application object.
    QtWebEngineQuick::initialize();
    MailWeb::registerSchemes();

    QGuiApplication app(argc, argv);
    QGuiApplication::setDesktopFileName(QStringLiteral("mailand-ui"));
    QQuickStyle::setStyle(QStringLiteral("Basic"));

    QQmlApplicationEngine engine;
    QObject::connect(&engine, &QQmlApplicationEngine::objectCreationFailed, &app,
                     [] { QCoreApplication::exit(1); }, Qt::QueuedConnection);
    if (argValue(argc, argv, "--webtest").isEmpty())
        engine.loadFromModule("Mailand", "Main");

    // Debug: load one message through the real mailand-msg:/cid: pipeline and report on it.
    if (const QString id = argValue(argc, argv, "--webtest"); !id.isEmpty()) {
        const QByteArray qml = QByteArray(R"QML(
import QtQuick
import QtWebEngine
import Mailand
Window {
    visible: true; width: 400; height: 300
    WebEngineView {
        id: v; anchors.fill: parent; profile: MailWeb.profile
        url: "mailand-msg://)QML") + id.toUtf8() + QByteArray(R"QML(/"
        onLoadingChanged: function(r) {
            if (r.status !== WebEngineView.LoadSucceededStatus) {
                if (r.status === WebEngineView.LoadFailedStatus) { console.log("webtest: load failed", r.errorString); Qt.exit(1) }
                return
            }
            v.runJavaScript("JSON.stringify({meta: !!document.querySelector('meta[http-equiv=Content-Security-Policy]'), imgs: [...document.images].map(i => [i.src, i.naturalWidth, i.complete]), text: document.body.innerText.slice(0, 40), probe: (window.__p = 'pending', fetch(location.href).then(() => window.__p = 'ALLOWED', () => window.__p = 'blocked by CSP'), 1)})",
                function(res) { console.log("webtest:", res); probeTimer.start() })
        }
    }
    Timer { id: probeTimer; interval: 500; onTriggered: v.runJavaScript("window.__p", function(r) { console.log("webtest: fetch probe:", r); Qt.exit(0) }) }
    Connections { target: MailWeb; function onRemoteContentBlocked() { console.log("webtest: remoteContentBlocked") } }
    Timer { interval: 8000; running: true; onTriggered: { console.log("webtest: timeout"); Qt.exit(1) } }
}
)QML");
        QObject::connect(&engine, &QQmlApplicationEngine::warnings, &app, [](const QList<QQmlError> &w) {
            for (const auto &e : w)
                QTextStream(stderr) << e.toString() << "\n";
        });
        engine.loadData(qml, QUrl(QStringLiteral("qrc:/webtest.qml")));
        return app.exec();
    }

    const QString shot = argValue(argc, argv, "--screenshot");
    if (!shot.isEmpty()) {
        bool ok = false;
        int delay = argValue(argc, argv, "--delay").toInt(&ok);
        if (!ok)
            delay = 3000;
        const bool openFirst = hasArg(argc, argv, "--open-first");
        const QString composerMode = argValue(argc, argv, "--open-composer");
        auto grab = [&engine, shot] {
            auto *win = engine.rootObjects().isEmpty() ? nullptr
                                                       : qobject_cast<QQuickWindow *>(engine.rootObjects().first());
            if (!win) {
                QTextStream(stderr) << "mailand-ui: --screenshot: no root window\n";
                QCoreApplication::exit(1);
                return;
            }
            const QImage img = win->grabWindow();
            if (img.isNull() || !img.save(shot)) {
                QTextStream(stderr) << "mailand-ui: --screenshot: could not save " << shot << "\n";
                QCoreApplication::exit(1);
                return;
            }
            QTextStream(stdout) << "screenshot " << shot << " " << img.width() << "x" << img.height() << "\n";
            QCoreApplication::quit();
        };
        QTimer::singleShot(delay, &app, [&engine, openFirst, composerMode, grab] {
            if (openFirst && !engine.rootObjects().isEmpty())
                QMetaObject::invokeMethod(engine.rootObjects().first(), "openFirstMessage");
            // Give the opened message time to load and render.
            QTimer::singleShot(openFirst ? 2000 : 0, QCoreApplication::instance(), [&engine, composerMode, grab] {
                if (composerMode.isEmpty() || engine.rootObjects().isEmpty()) {
                    grab();
                    return;
                }
                QMetaObject::invokeMethod(engine.rootObjects().first(), "debugOpenComposer",
                                          Q_ARG(QString, composerMode));
                QTimer::singleShot(1500, QCoreApplication::instance(), grab);
            });
        });
    }
    return app.exec();
}
