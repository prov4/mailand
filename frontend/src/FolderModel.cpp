#include "FolderModel.h"

#include <algorithm>

#include "Rpc.h"

namespace {

struct Classified {
    int rank;
    QString display;
    QString icon;
};

Classified classify(const QString &name, const QString &path, const QString &kind, const QString &su)
{
    const QString lsu = su.toLower();
    if (lsu == QLatin1String("\\inbox") || path.compare(QLatin1String("INBOX"), Qt::CaseInsensitive) == 0)
        return {0, QStringLiteral("Inbox"), QStringLiteral("inbox")};
    if (lsu == QLatin1String("\\flagged"))
        return {1, QStringLiteral("Starred"), QStringLiteral("star")};
    if (lsu == QLatin1String("\\sent"))
        return {2, QStringLiteral("Sent"), QStringLiteral("sent")};
    if (lsu == QLatin1String("\\drafts"))
        return {3, QStringLiteral("Drafts"), QStringLiteral("drafts")};
    if (lsu == QLatin1String("\\all"))
        return {4, QStringLiteral("All Mail"), QStringLiteral("archive")};
    if (lsu == QLatin1String("\\junk"))
        return {5, QStringLiteral("Spam"), QStringLiteral("spam")};
    if (lsu == QLatin1String("\\trash"))
        return {6, QStringLiteral("Trash"), QStringLiteral("trash")};

    QString display = name;
    if (display.startsWith(QLatin1String("[Gmail]/")))
        display = display.mid(8);
    if (lsu == QLatin1String("\\archive"))
        return {7, display.isEmpty() ? QStringLiteral("Archive") : display, QStringLiteral("archive")};
    return {7, display, kind == QLatin1String("gmail_label") ? QStringLiteral("label") : QStringLiteral("folder")};
}

} // namespace

bool FolderModel::Entry::sameAs(const Entry &o) const
{
    return id == o.id && name == o.name && path == o.path && kind == o.kind
           && specialUse == o.specialUse && unread == o.unread;
}

FolderModel::FolderModel(QObject *parent) : QAbstractListModel(parent)
{
    m_debounce.setSingleShot(true);
    m_debounce.setInterval(300);
    connect(&m_debounce, &QTimer::timeout, this, &FolderModel::refresh);
    connect(Rpc::instance(), &Rpc::daemonEvent, this,
            [this](int type, const QString &account, const QString &, const QString &) {
                // 2 NEW_MESSAGES, 3 FLAGS_UPDATED
                if ((type == 2 || type == 3) && !m_accountId.isEmpty() && account == m_accountId)
                    m_debounce.start();
            });
}

int FolderModel::rowCount(const QModelIndex &parent) const
{
    return parent.isValid() ? 0 : int(m_items.size());
}

QVariant FolderModel::data(const QModelIndex &index, int role) const
{
    if (!index.isValid() || index.row() < 0 || index.row() >= int(m_items.size()))
        return {};
    const Entry &e = m_items[index.row()];
    switch (role) {
    case FolderIdRole: return e.id;
    case NameRole: return e.name;
    case PathRole: return e.path;
    case UnreadCountRole: return e.unread;
    case KindRole: return e.kind;
    case SpecialUseRole: return e.specialUse;
    case DisplayNameRole: return e.displayName;
    case IconRole: return e.icon;
    default: return {};
    }
}

QHash<int, QByteArray> FolderModel::roleNames() const
{
    return {
        {FolderIdRole, "folderId"}, {NameRole, "name"}, {PathRole, "path"},
        {UnreadCountRole, "unreadCount"}, {KindRole, "kind"}, {SpecialUseRole, "specialUse"},
        {DisplayNameRole, "displayName"}, {IconRole, "icon"},
    };
}

void FolderModel::setAccountId(const QString &id)
{
    if (id == m_accountId)
        return;
    m_accountId = id;
    emit accountIdChanged();
    ++m_gen;
    if (!m_items.empty()) {
        beginResetModel();
        m_items.clear();
        endResetModel();
        emit countChanged();
    }
    refresh();
}

QString FolderModel::folderIdAt(int row) const
{
    return row >= 0 && row < int(m_items.size()) ? m_items[row].id : QString();
}

int FolderModel::indexOfFolder(const QString &folderId) const
{
    for (size_t i = 0; i < m_items.size(); ++i)
        if (m_items[i].id == folderId)
            return int(i);
    return -1;
}

void FolderModel::refresh()
{
    if (m_accountId.isEmpty())
        return;
    const int gen = ++m_gen;
    // Replies from older generations are dropped; the newest one resets the pending state.
    m_pending = 1;
    emit loadingChanged();
    Rpc::instance()->listFolders(
        m_accountId, this, [this, gen](const grpc::Status &st, const mailand::v1::FolderList &list) {
            if (gen != m_gen)
                return;
            m_pending = 0;
            emit loadingChanged();
            if (!st.ok()) {
                Rpc::instance()->reportError(QStringLiteral("Loading folders failed: ") + Rpc::statusText(st));
                return;
            }
            std::vector<Entry> fresh;
            for (const auto &f : list.folders()) {
                Entry e;
                e.id = QString::fromStdString(f.id());
                e.name = QString::fromStdString(f.name());
                e.path = QString::fromStdString(f.path());
                e.kind = QString::fromStdString(f.kind());
                e.specialUse = QString::fromStdString(f.special_use());
                e.unread = int(f.unread_count());
                const Classified c = classify(e.name, e.path, e.kind, e.specialUse);
                e.rank = c.rank;
                e.displayName = c.display;
                e.icon = c.icon;
                fresh.push_back(std::move(e));
            }
            std::stable_sort(fresh.begin(), fresh.end(), [](const Entry &a, const Entry &b) {
                if (a.rank != b.rank)
                    return a.rank < b.rank;
                return a.displayName.compare(b.displayName, Qt::CaseInsensitive) < 0;
            });
            apply(std::move(fresh));
        });
}

void FolderModel::apply(std::vector<Entry> fresh)
{
    const int oldCount = count();

    // 1. drop rows that disappeared
    for (int i = int(m_items.size()) - 1; i >= 0; --i) {
        const bool keep = std::any_of(fresh.begin(), fresh.end(),
                                      [&](const Entry &e) { return e.id == m_items[i].id; });
        if (!keep) {
            beginRemoveRows({}, i, i);
            m_items.erase(m_items.begin() + i);
            endRemoveRows();
        }
    }
    // 2. walk the target order: keep / move / insert
    for (int i = 0; i < int(fresh.size()); ++i) {
        if (i < int(m_items.size()) && m_items[i].id == fresh[i].id) {
            if (!m_items[i].sameAs(fresh[i]) || m_items[i].displayName != fresh[i].displayName
                || m_items[i].icon != fresh[i].icon) {
                m_items[i] = fresh[i];
                emit dataChanged(index(i), index(i));
            }
            continue;
        }
        int from = -1;
        for (int j = i + 1; j < int(m_items.size()); ++j)
            if (m_items[j].id == fresh[i].id) { from = j; break; }
        if (from >= 0) {
            beginMoveRows({}, from, from, {}, i);
            std::rotate(m_items.begin() + i, m_items.begin() + from, m_items.begin() + from + 1);
            endMoveRows();
            m_items[i] = fresh[i];
            emit dataChanged(index(i), index(i));
        } else {
            beginInsertRows({}, i, i);
            m_items.insert(m_items.begin() + i, fresh[i]);
            endInsertRows();
        }
    }
    if (oldCount != count())
        emit countChanged();
}
