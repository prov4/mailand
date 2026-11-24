#include <QCoreApplication>
#include <QGuiApplication>
#include <QQmlApplicationEngine>
#include <QtQuickControls2/QQuickStyle>
#include <QTextStream>

#include <cstring>

#include "RpcClient.h"

static bool hasArg(int argc, char **argv, const char *flag)
{
    for (int i = 1; i < argc; ++i)
        if (std::strcmp(argv[i], flag) == 0)
            return true;
    return false;
}

// Headless: no display needed, so it uses QCoreApplication only.
static int runPrint(int argc, char **argv)
{
    QCoreApplication app(argc, argv);
    QList<AccountInfo> accounts;
    QString error;
    if (!RpcClient::listAccountsSync(&accounts, &error)) {
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

    if (qEnvironmentVariableIsEmpty("QT_QPA_PLATFORM"))
        qputenv("QT_QPA_PLATFORM", "wayland");

    QGuiApplication app(argc, argv);
    QGuiApplication::setDesktopFileName(QStringLiteral("mailand-ui"));
    QQuickStyle::setStyle(QStringLiteral("Basic"));

    QQmlApplicationEngine engine;
    QObject::connect(&engine, &QQmlApplicationEngine::objectCreationFailed, &app,
                     [] { QCoreApplication::exit(1); }, Qt::QueuedConnection);
    engine.loadFromModule("Mailand", "Main");
    return app.exec();
}
