#include "MessageModel.h"

#include <QHash>
#include <QSet>

#include <algorithm>

#include "Format.h"
#include "Rpc.h"

namespace {
constexpr quint32 kPage = 100;
constexpr quint32 kSearchLimit = 200;
constexpr quint32 kRefreshCap = 1000;
} // namespace

bool MessageModel::Item::sameAs(const Item &o) const
{
    return id == o.id && subject == o.subject && fromName == o.fromName
           && fromAddress == o.fromAddress && snippetRaw == o.snippetRaw && ts == o.ts
           && isRead == o.isRead && isFlagged == o.isFlagged && isAnswered == o.isAnswered && hasAttachments == o.hasAttachments;
}

MessageModel::Item MessageModel::fromProto(const void *p)
{
    const auto &h = *static_cast<const mailand::v1::MessageHeader *>(p);
    Item it;
    it.id = QString::fromStdString(h.id());
    it.subject = QString::fromStdString(h.subject());
    it.fromName = QString::fromStdString(h.from_name());
    it.fromAddress = QString::fromStdString(h.from_address());
    it.snippetRaw = QString::fromStdString(h.snippet());
    it.ts = h.timestamp_unix();
    it.isRead = h.is_read();
    it.isFlagged = h.is_flagged();
    it.isAnswered = h.is_answered();
    it.hasAttachments = h.has_attachments();
    return it;
}

MessageModel::MessageModel(QObject *parent) : QAbstractListModel(parent)
{
    m_searchDebounce.setSingleShot(true);
    m_searchDebounce.setInterval(150);
    connect(&m_searchDebounce, &QTimer::timeout, this, &MessageModel::refresh);
    m_eventDebounce.setSingleShot(true);
    m_eventDebounce.setInterval(300);
    connect(&m_eventDebounce, &QTimer::timeout, this, &MessageModel::refresh);

    connect(Rpc::instance(), &Rpc::daemonEvent, this,
            [this](int type, const QString &account, const QString &, const QString &) {
                if ((type != 2 && type != 3) || (m_folderId.isEmpty() && !searchMode()))
                    return;
                // Throttle (see FolderModel): refresh at most every 300 ms while events stream in.
                if ((m_accountId.isEmpty() || account == m_accountId) && !m_eventDebounce.isActive())
                    m_eventDebounce.start();
            });
}

int MessageModel::rowCount(const QModelIndex &parent) const
{
    return parent.isValid() ? 0 : int(m_items.size());
}

QVariant MessageModel::data(const QModelIndex &index, int role) const
{
    if (!index.isValid() || index.row() < 0 || index.row() >= int(m_items.size()))
        return {};
    const Item &m = m_items[index.row()];
    switch (role) {
    case MessageIdRole: return m.id;
    case SubjectRole: return m.subject.isEmpty() ? QStringLiteral("(no subject)") : m.subject;
    case FromNameRole: return m.fromName;
    case FromAddressRole: return m.fromAddress;
    case SenderRole: return m.fromName.isEmpty() ? m.fromAddress : m.fromName;
    case TimestampRole: return QDateTime::fromSecsSinceEpoch(m.ts);
    case DateTextRole: return Format::shortDate(m.ts);
    case IsReadRole: return m.isRead;
    case IsFlaggedRole: return m.isFlagged;
    case IsAnsweredRole: return m.isAnswered;
    case HasAttachmentsRole: return m.hasAttachments;
    case SnippetRole: return Format::snippetPlain(m.snippetRaw);
    case SnippetRichRole: return Format::snippetRich(m.snippetRaw);
    default: return {};
    }
}

QHash<int, QByteArray> MessageModel::roleNames() const
{
    return {
        {MessageIdRole, "messageId"}, {SubjectRole, "subject"}, {FromNameRole, "fromName"},
        {FromAddressRole, "fromAddress"}, {SenderRole, "sender"}, {TimestampRole, "timestamp"},
        {DateTextRole, "dateText"}, {IsReadRole, "isRead"}, {IsFlaggedRole, "isFlagged"},
        {IsAnsweredRole, "isAnswered"}, {HasAttachmentsRole, "hasAttachments"}, {SnippetRole, "snippet"},
        {SnippetRichRole, "snippetRich"},
    };
}

void MessageModel::setLoading(bool l)
{
    if (m_loading == l)
        return;
    m_loading = l;
    emit loadingChanged();
}

void MessageModel::setTotal(int t)
{
    if (m_total == t)
        return;
    m_total = t;
    emit totalCountChanged();
}

void MessageModel::setFolderId(const QString &id)
{
    if (id == m_folderId)
        return;
    m_folderId = id;
    emit folderIdChanged();
    ++m_gen; // drop everything in flight for the old folder
    if (!searchMode()) {
        if (!m_items.empty()) {
            beginResetModel();
            m_items.clear();
            endResetModel();
            emit countChanged();
        }
        setTotal(0);
        refresh();
    }
}

void MessageModel::setAccountId(const QString &id)
{
    if (id == m_accountId)
        return;
    m_accountId = id;
    emit accountIdChanged();
    if (searchMode())
        m_searchDebounce.start();
}

void MessageModel::setSearchQuery(const QString &q)
{
    if (q == m_query)
        return;
    m_query = q;
    emit searchQueryChanged();
    ++m_gen; // anything still in flight belongs to the previous query
    if (searchMode()) {
        m_searchDebounce.start();
    } else {
        m_searchDebounce.stop();
        refresh(); // back to folder mode, immediately
    }
}

void MessageModel::setUnreadOnly(bool on)
{
    if (on == m_unreadOnly)
        return;
    m_unreadOnly = on;
    emit unreadOnlyChanged();
    ++m_gen;
    if (!m_items.empty()) {
        beginResetModel();
        m_items.clear();
        endResetModel();
        emit countChanged();
    }
    setTotal(0);
    refresh();
}

bool MessageModel::canFetchMore(const QModelIndex &parent) const
{
    return !parent.isValid() && !searchMode() && !m_folderId.isEmpty() && !m_loading
           && int(m_items.size()) < m_total;
}

void MessageModel::fetchMore(const QModelIndex &parent)
{
    if (canFetchMore(parent))
        load(true);
}

void MessageModel::refresh()
{
    m_searchDebounce.stop();
    m_eventDebounce.stop();
    load(false);
}

void MessageModel::load(bool appendPage)
{
    if (!searchMode() && m_folderId.isEmpty())
        return;
    const int gen = ++m_gen;
    setLoading(true);
    Rpc *rpc = Rpc::instance();

    auto onDone = [this, gen, appendPage](const grpc::Status &st, const mailand::v1::MessageHeaderList &list) {
        if (gen != m_gen)
            return; // stale reply (newer query / folder / refresh superseded it)
        setLoading(false);
        if (!st.ok()) {
            Rpc::instance()->reportError(QStringLiteral("Loading messages failed: ") + Rpc::statusText(st));
            return;
        }
        std::vector<Item> fresh;
        fresh.reserve(list.messages_size());
        for (const auto &h : list.messages())
            fresh.push_back(fromProto(&h));
        if (searchMode()) {
            setTotal(int(fresh.size()));
        } else {
            setTotal(int(list.total_count()));
        }
        if (appendPage)
            append(std::move(fresh));
        else
            mergeReplace(std::move(fresh));
    };

    if (searchMode()) {
        rpc->searchMessages(m_query, m_accountId, kSearchLimit, m_unreadOnly, this, std::move(onDone));
    } else if (appendPage) {
        rpc->listMessages(m_folderId, kPage, quint32(m_items.size()), m_unreadOnly, this, std::move(onDone));
    } else {
        const quint32 limit = std::clamp<quint32>(quint32(m_items.size()), kPage, kRefreshCap);
        rpc->listMessages(m_folderId, limit, 0, m_unreadOnly, this, std::move(onDone));
    }
}

void MessageModel::append(std::vector<Item> more)
{
    // Skip anything already present (rows may have shifted between pages).
    QSet<QString> have;
    for (const Item &i : m_items)
        have.insert(i.id);
    std::erase_if(more, [&](const Item &i) { return have.contains(i.id); });
    if (more.empty())
        return;
    const int first = int(m_items.size());
    beginInsertRows({}, first, first + int(more.size()) - 1);
    m_items.insert(m_items.end(), more.begin(), more.end());
    endInsertRows();
    emit countChanged();
}

void MessageModel::mergeReplace(std::vector<Item> fresh)
{
    const int oldCount = count();
    QSet<QString> want;
    for (const Item &i : fresh)
        want.insert(i.id);

    // 1. remove rows that are gone (back to front, coalescing runs)
    for (int i = int(m_items.size()) - 1; i >= 0;) {
        if (want.contains(m_items[i].id)) { --i; continue; }
        int last = i;
        while (i >= 0 && !want.contains(m_items[i].id))
            --i;
        beginRemoveRows({}, i + 1, last);
        m_items.erase(m_items.begin() + i + 1, m_items.begin() + last + 1);
        endRemoveRows();
    }

    // 2. walk target order: keep / move / insert (runs of new rows inserted at once)
    QSet<QString> haveIds;
    for (const Item &i : m_items)
        haveIds.insert(i.id);
    for (int i = 0; i < int(fresh.size());) {
        if (i < int(m_items.size()) && m_items[i].id == fresh[i].id) {
            if (!m_items[i].sameAs(fresh[i])) {
                m_items[i] = fresh[i];
                emit dataChanged(index(i), index(i));
            }
            ++i;
            continue;
        }
        if (haveIds.contains(fresh[i].id)) {
            int from = -1;
            for (int j = i + 1; j < int(m_items.size()); ++j)
                if (m_items[j].id == fresh[i].id) { from = j; break; }
            if (from < 0) { ++i; continue; } // cannot happen
            beginMoveRows({}, from, from, {}, i);
            std::rotate(m_items.begin() + i, m_items.begin() + from, m_items.begin() + from + 1);
            endMoveRows();
            if (!m_items[i].sameAs(fresh[i])) {
                m_items[i] = fresh[i];
                emit dataChanged(index(i), index(i));
            }
            ++i;
        } else {
            int run = 0;
            while (i + run < int(fresh.size()) && !haveIds.contains(fresh[i + run].id))
                ++run;
            beginInsertRows({}, i, i + run - 1);
            m_items.insert(m_items.begin() + i, fresh.begin() + i, fresh.begin() + i + run);
            endInsertRows();
            for (int k = i; k < i + run; ++k)
                haveIds.insert(m_items[k].id);
            i += run;
        }
    }
    if (oldCount != count())
        emit countChanged();
}

int MessageModel::indexOfMessage(const QString &id) const
{
    for (size_t i = 0; i < m_items.size(); ++i)
        if (m_items[i].id == id)
            return int(i);
    return -1;
}

QVariant MessageModel::get(int row) const
{
    if (row < 0 || row >= int(m_items.size()))
        return QVariantMap();
    QVariantMap m;
    const auto names = roleNames();
    for (auto it = names.cbegin(); it != names.cend(); ++it)
        m.insert(QString::fromUtf8(it.value()), data(index(row), it.key()));
    return m;
}

void MessageModel::emitRow(int row, const QList<int> &roles)
{
    emit dataChanged(index(row), index(row), roles);
}

void MessageModel::setRead(int row, bool read)
{
    if (row < 0 || row >= int(m_items.size()))
        return;
    setFlag(m_items[row].id, true, read);
}

void MessageModel::toggleRead(int row)
{
    if (row < 0 || row >= int(m_items.size()))
        return;
    setFlag(m_items[row].id, true, !m_items[row].isRead);
}

void MessageModel::toggleFlagged(int row)
{
    if (row < 0 || row >= int(m_items.size()))
        return;
    setFlag(m_items[row].id, false, !m_items[row].isFlagged);
}

void MessageModel::archive(int row)
{
    if (row < 0 || row >= int(m_items.size()))
        return;
    const QString id = m_items[row].id;
    beginRemoveRows({}, row, row);
    m_items.erase(m_items.begin() + row);
    endRemoveRows();
    if (m_total > 0)
        setTotal(m_total - 1);
    emit countChanged();

    Rpc::instance()->archiveMessage(id, this, [this](const grpc::Status &st, const mailand::v1::Empty &) {
        if (st.ok())
            return;
        refresh();
        Rpc::instance()->reportError(QStringLiteral("Could not archive message: ") + Rpc::statusText(st));
    });
}

// Optimistic: update the row now, send SetMessageFlags, revert on failure.
void MessageModel::setFlag(const QString &id, bool isReadChange, bool value)
{
    const int row = indexOfMessage(id);
    if (row < 0)
        return;
    bool &flag = isReadChange ? m_items[row].isRead : m_items[row].isFlagged;
    if (flag == value)
        return;
    flag = value;
    emitRow(row, {isReadChange ? int(IsReadRole) : int(IsFlaggedRole)});

    Rpc::instance()->setFlags(
        id, isReadChange ? std::optional<bool>(value) : std::nullopt,
        isReadChange ? std::nullopt : std::optional<bool>(value), this,
        [this, id, isReadChange, value](const grpc::Status &st, const mailand::v1::Empty &) {
            if (st.ok())
                return;
            const int r = indexOfMessage(id);
            if (r >= 0) {
                bool &f = isReadChange ? m_items[r].isRead : m_items[r].isFlagged;
                if (f == value) { // nobody changed it since
                    f = !value;
                    emitRow(r, {isReadChange ? int(IsReadRole) : int(IsFlaggedRole)});
                }
            }
            Rpc::instance()->reportError(QStringLiteral("Could not update message: ") + Rpc::statusText(st));
        });
}
