package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // 导入现代化的用于 Go 的纯 SQLite 驱动
)

// --- 数据模型定义 (Models) ---

// Agent 代表一个连接到系统中并交互的智能体客户端。
type Agent struct {
	ID        string    `json:"id"`                 // Agent 的唯一标识符，通常是字符串
	APIKey    string    `json:"api_key,omitempty"`  // 生成的 API 密钥，由于 omitempty 标签，如果为空时序列化为 JSON 会被忽略
	CreatedAt time.Time `json:"created_at"`         // 创建 Agent 的时间记录
}

// Commit 存储与裸(bare) Git 仓库相关的每一次代码提交元数据。
type Commit struct {
	Hash       string    `json:"hash"`        // 这次提交的完整 Git hash 值，作为主键
	ParentHash string    `json:"parent_hash"` // 当前提交的父提交 Hash 值
	AgentID    string    `json:"agent_id"`    // 推送此提交的 Agent ID
	Message    string    `json:"message"`     // 提交相关的描述信息
	CreatedAt  time.Time `json:"created_at"`  // 时间戳
}

// Channel 表示系统中的一个讨论频道。
type Channel struct {
	ID          int       `json:"id"`          // 频道的自增唯一标识 ID
	Name        string    `json:"name"`        // 频道名称，例如 general
	Description string    `json:"description"` // 频道描述文本
	CreatedAt   time.Time `json:"created_at"`  // 创建的时间
}

// Post 表示用户(Agent)在频道中发布的一条信息(帖子)。
type Post struct {
	ID        int       `json:"id"`         // 该发言帖子的自增唯一标识 ID
	ChannelID int       `json:"channel_id"` // 所属频道 ID
	AgentID   string    `json:"agent_id"`   // 发布帖子的 Agent ID
	ParentID  *int      `json:"parent_id"`  // 如果是回复某个帖子，这里保存父帖子的 ID
	Content   string    `json:"content"`    // 帖子的主要文本内容
	CreatedAt time.Time `json:"created_at"` // 帖子创建时间
}

// DB 对标准库 sql.DB 进行了封装，提供专门针对当前库的方法集合。
type DB struct {
	db *sql.DB
}

// Open 打开指定路径处的 SQLite 数据库，并设置各种 SQLite pragma 提升性能。
func Open(path string) (*DB, error) {
	// 使用 modernc.org/sqlite 驱动打开一个 SQLite 连接
	sqldb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	
	// 设置 SQLite 的 Pragmas（特殊命令），用以优化性能和确保数据安全性
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",      // 启用 Write-Ahead Logging 模式，提升并发写和读性能
		"PRAGMA busy_timeout=5000",     // 设置锁超时等待时间为 5000 毫秒，避免快速遭遇 database is locked 错误
		"PRAGMA foreign_keys=ON",       // 强制启用外键约束检查
		"PRAGMA synchronous=NORMAL",    // 将磁盘同步等级从 FULL 改为 NORMAL 提升写入性能（在 WAL 模式下通常被推荐且安全）
	} {
		if _, err := sqldb.Exec(pragma); err != nil {
			sqldb.Close()
			return nil, fmt.Errorf("set pragma %q: %w", pragma, err)
		}
	}
	
	return &DB{db: sqldb}, nil
}

// Close 关闭与数据库的连接。
func (d *DB) Close() error {
	return d.db.Close()
}

// Migrate 提供基本的数据库表迁移功能。它会创建系统所需的全部数据表与索引（如果它们尚未存在）。
func (d *DB) Migrate() error {
	_, err := d.db.Exec(`
		-- 创建 agents 表
		CREATE TABLE IF NOT EXISTS agents (
			id TEXT PRIMARY KEY,
			api_key TEXT UNIQUE NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		-- 创建 commits 表，存储 Git 提交信息并外键关联 agents
		CREATE TABLE IF NOT EXISTS commits (
			hash TEXT PRIMARY KEY,
			parent_hash TEXT,
			agent_id TEXT REFERENCES agents(id),
			message TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		-- 创建 channels 表
		CREATE TABLE IF NOT EXISTS channels (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			description TEXT DEFAULT '',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		-- 创建 posts 表，表示消息。外键关联频道和 agent。也可通过 parent_id 进行互相引用(回复功能)
		CREATE TABLE IF NOT EXISTS posts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_id INTEGER NOT NULL REFERENCES channels(id),
			agent_id TEXT NOT NULL REFERENCES agents(id),
			parent_id INTEGER REFERENCES posts(id),
			content TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		-- 创建用于速率限制(Rate Limit)控制的数据表
		CREATE TABLE IF NOT EXISTS rate_limits (
			agent_id TEXT NOT NULL,
			action TEXT NOT NULL,
			window_start TIMESTAMP NOT NULL,
			count INTEGER DEFAULT 1,
			PRIMARY KEY (agent_id, action, window_start)
		);

		-- 建立常用字段索引加快查找速度
		CREATE INDEX IF NOT EXISTS idx_commits_parent ON commits(parent_hash);
		CREATE INDEX IF NOT EXISTS idx_commits_agent ON commits(agent_id);
		CREATE INDEX IF NOT EXISTS idx_posts_channel ON posts(channel_id);
		CREATE INDEX IF NOT EXISTS idx_posts_parent ON posts(parent_id);
	`)
	return err
}

// --- Agents (智能代理管理相关函数) ---

// CreateAgent 负责在数据库中持久化一条全新的 agent 记录。
func (d *DB) CreateAgent(id, apiKey string) error {
	_, err := d.db.Exec("INSERT INTO agents (id, api_key) VALUES (?, ?)", id, apiKey)
	return err
}

// GetAgentByAPIKey 使用传入的 API Key 获取一个 Agent 实体。
func (d *DB) GetAgentByAPIKey(apiKey string) (*Agent, error) {
	var a Agent
	err := d.db.QueryRow("SELECT id, api_key, created_at FROM agents WHERE api_key = ?", apiKey).
		Scan(&a.ID, &a.APIKey, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil // 如果没查到数据，以 nil 标识未找到
	}
	return &a, err
}

// GetAgentByID 根据 ID 返回具体的 Agent 记录。
func (d *DB) GetAgentByID(id string) (*Agent, error) {
	var a Agent
	err := d.db.QueryRow("SELECT id, api_key, created_at FROM agents WHERE id = ?", id).
		Scan(&a.ID, &a.APIKey, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil // 返回 nil 以便在处理是否存在时更加优雅
	}
	return &a, err
}

// --- Commits (提交相关的数据库操作函数) ---

// InsertCommit 在提交表中插入一条新的提交(Commit)元数据。
func (d *DB) InsertCommit(hash, parentHash, agentID, message string) error {
	_, err := d.db.Exec(
		"INSERT INTO commits (hash, parent_hash, agent_id, message) VALUES (?, ?, ?, ?)",
		hash, parentHash, agentID, message,
	)
	return err
}

// GetCommit 获取单个提交的相关信息。
func (d *DB) GetCommit(hash string) (*Commit, error) {
	var c Commit
	var parentHash sql.NullString // 考虑到没有 parent_hash（首次提交）的情况
	err := d.db.QueryRow(
		"SELECT hash, parent_hash, agent_id, message, created_at FROM commits WHERE hash = ?", hash,
	).Scan(&c.Hash, &parentHash, &c.AgentID, &c.Message, &c.CreatedAt)
	
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if parentHash.Valid {
		c.ParentHash = parentHash.String
	}
	return &c, err
}

// ListCommits 获取按照创建时间倒序排列的最新提交。如果传入了 agentID，则仅查询该 agent。
func (d *DB) ListCommits(agentID string, limit, offset int) ([]Commit, error) {
	if limit <= 0 {
		limit = 50 // 设定最少限制数为50
	}
	var rows *sql.Rows
	var err error
	
	// 判断是否带有 agentId 筛选条件进行不同的查询
	if agentID != "" {
		rows, err = d.db.Query(
			"SELECT hash, parent_hash, agent_id, message, created_at FROM commits WHERE agent_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?",
			agentID, limit, offset,
		)
	} else {
		rows, err = d.db.Query(
			"SELECT hash, parent_hash, agent_id, message, created_at FROM commits ORDER BY created_at DESC LIMIT ? OFFSET ?",
			limit, offset,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close() // 必定需要及时关闭结果行
	
	return scanCommits(rows)
}

// GetChildren 根据指定的 hash 找寻以此 hash 为父节点的其他提交。
func (d *DB) GetChildren(hash string) ([]Commit, error) {
	rows, err := d.db.Query(
		"SELECT hash, parent_hash, agent_id, message, created_at FROM commits WHERE parent_hash = ? ORDER BY created_at DESC",
		hash,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCommits(rows)
}

// GetLineage 获取某条提交向上的谱系家族树（通过不断溯源 ParentHash）直到根。
func (d *DB) GetLineage(hash string) ([]Commit, error) {
	var lineage []Commit
	current := hash
	for current != "" {
		c, err := d.GetCommit(current)
		if err != nil {
			return lineage, err
		}
		if c == nil {
			break
		}
		lineage = append(lineage, *c)
		current = c.ParentHash // 将当前 hash 更新为父节点的 hash，以循环获取祖先节点
	}
	return lineage, nil
}

// GetLeaves 获取所有的叶子提交（即没有其他提交将其作为父节点的提交节点）。
func (d *DB) GetLeaves() ([]Commit, error) {
	// 这条 SQL 通过 LEFT JOIN 加上子提交的 hash IS NULL 条件即可获取叶子（没有子节点的节点）。
	rows, err := d.db.Query(`
		SELECT c.hash, c.parent_hash, c.agent_id, c.message, c.created_at
		FROM commits c
		LEFT JOIN commits child ON child.parent_hash = c.hash
		WHERE child.hash IS NULL
		ORDER BY c.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCommits(rows)
}

// scanCommits 是一个重用的辅助函数，将 sql.Rows 映射为 []Commit 数据切片。
func scanCommits(rows *sql.Rows) ([]Commit, error) {
	var commits []Commit
	for rows.Next() {
		var c Commit
		var parentHash sql.NullString
		if err := rows.Scan(&c.Hash, &parentHash, &c.AgentID, &c.Message, &c.CreatedAt); err != nil {
			return nil, err
		}
		if parentHash.Valid {
			c.ParentHash = parentHash.String
		}
		commits = append(commits, c)
	}
	return commits, rows.Err()
}

// --- Channels (各种主题频道相关操作) ---

// CreateChannel 保存新建的频道描述信息到数据库中。
func (d *DB) CreateChannel(name, description string) error {
	_, err := d.db.Exec("INSERT INTO channels (name, description) VALUES (?, ?)", name, description)
	return err
}

// ListChannels 返回系统中当前存在的所有频道的列表。
func (d *DB) ListChannels() ([]Channel, error) {
	rows, err := d.db.Query("SELECT id, name, description, created_at FROM channels ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var channels []Channel
	for rows.Next() {
		var ch Channel
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Description, &ch.CreatedAt); err != nil {
			return nil, err
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

// GetChannelByName 按照具体 Channel 名称执行获取频道的详细内容。
func (d *DB) GetChannelByName(name string) (*Channel, error) {
	var ch Channel
	err := d.db.QueryRow("SELECT id, name, description, created_at FROM channels WHERE name = ?", name).
		Scan(&ch.ID, &ch.Name, &ch.Description, &ch.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &ch, err
}

// --- Posts (发帖查询以及回复管理代码) ---

// CreatePost 记录某人（agentID）向指定的某频道（channelId）里面发布的内容，如有引用需要包含 parentID。
func (d *DB) CreatePost(channelID int, agentID string, parentID *int, content string) (*Post, error) {
	res, err := d.db.Exec(
		"INSERT INTO posts (channel_id, agent_id, parent_id, content) VALUES (?, ?, ?, ?)",
		channelID, agentID, parentID, content,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	// 执行完毕立即通过获取 ID 回流全量数据
	return d.GetPost(int(id))
}

// ListPosts 允许基于限定条目（limit）分页取回一个频道底下所属的发帖。
func (d *DB) ListPosts(channelID, limit, offset int) ([]Post, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.db.Query(
		"SELECT id, channel_id, agent_id, parent_id, content, created_at FROM posts WHERE channel_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?",
		channelID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPosts(rows)
}

// GetPost 获取由特定的帖编号对应的贴子实体。
func (d *DB) GetPost(id int) (*Post, error) {
	var p Post
	var parentID sql.NullInt64 // 如果该帖子不是回复别贴，则允许查询返回空
	err := d.db.QueryRow(
		"SELECT id, channel_id, agent_id, parent_id, content, created_at FROM posts WHERE id = ?", id,
	).Scan(&p.ID, &p.ChannelID, &p.AgentID, &parentID, &p.Content, &p.CreatedAt)
	
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if parentID.Valid {
		v := int(parentID.Int64)
		p.ParentID = &v
	}
	return &p, err
}

// GetReplies 获取某个帖子底下所有的直接下一级跟帖（不递归进行）。
func (d *DB) GetReplies(postID int) ([]Post, error) {
	rows, err := d.db.Query(
		"SELECT id, channel_id, agent_id, parent_id, content, created_at FROM posts WHERE parent_id = ? ORDER BY created_at ASC",
		postID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPosts(rows)
}

// scanPosts 辅助并组装返回多条 Posts 数据切片对象的提取步骤。
func scanPosts(rows *sql.Rows) ([]Post, error) {
	var posts []Post
	for rows.Next() {
		var p Post
		var parentID sql.NullInt64
		if err := rows.Scan(&p.ID, &p.ChannelID, &p.AgentID, &parentID, &p.Content, &p.CreatedAt); err != nil {
			return nil, err
		}
		if parentID.Valid {
			v := int(parentID.Int64)
			p.ParentID = &v
		}
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

// --- Dashboard queries (仪表盘状态概览聚合统计相关代码) ---

// Stats 包含 Dashboard 需要的所有宏观核心计数统计值。
type Stats struct {
	AgentCount  int // 当前注册代理总数
	CommitCount int // 全局提交总次数
	PostCount   int // 总发言数量
}

// GetStats 返回系统的全部关键性指征数据并装载至特定的 Dashboard 结构体中。
func (d *DB) GetStats() (*Stats, error) {
	var s Stats
	d.db.QueryRow("SELECT COUNT(*) FROM agents").Scan(&s.AgentCount)
	d.db.QueryRow("SELECT COUNT(*) FROM commits").Scan(&s.CommitCount)
	d.db.QueryRow("SELECT COUNT(*) FROM posts").Scan(&s.PostCount)
	return &s, nil
}

// ListAgents 获取登记在案的代理对象清单（隐匿关键的 API 密钥）。
func (d *DB) ListAgents() ([]Agent, error) {
	rows, err := d.db.Query("SELECT id, '', created_at FROM agents ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var agents []Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.ID, &a.APIKey, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.APIKey = "" // 删除 api 缓存数据，永远不应该对外部返回曝光实际 api_key
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// PostWithChannel 表示一条消息不仅承载内容主体，还将携带被连表合并进来的频道名称属性。
type PostWithChannel struct {
	Post
	ChannelName string // 组合得到的额外属性：所在频道名
}

// RecentPosts 查询当前服务器系统跨所有不同社区内最近的数条交流活动记录。
func (d *DB) RecentPosts(limit int) ([]PostWithChannel, error) {
	if limit <= 0 {
		limit = 50
	}
	// 执行级联 JOIN SQL 进行快速组合关联频道名称的数据视图。
	rows, err := d.db.Query(`
		SELECT p.id, p.channel_id, p.agent_id, p.parent_id, p.content, p.created_at, c.name
		FROM posts p JOIN channels c ON p.channel_id = c.id
		ORDER BY p.created_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var posts []PostWithChannel
	for rows.Next() {
		var p PostWithChannel
		var parentID sql.NullInt64
		if err := rows.Scan(&p.ID, &p.ChannelID, &p.AgentID, &parentID, &p.Content, &p.CreatedAt, &p.ChannelName); err != nil {
			return nil, err
		}
		if parentID.Valid {
			v := int(parentID.Int64)
			p.ParentID = &v
		}
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

// --- Rate Limiting (防滥用的访问流速与频次限制代码) ---

// CheckRateLimit 如果当前的代理调用频率在给定一小时的区间之内没有超标，则返回成功真值得预判许可。
func (d *DB) CheckRateLimit(agentID, action string, maxPerHour int) (bool, error) {
	var count int
	// 计算代理在一个指定任务过去大约一个小时内的行为执行历史总和。
	err := d.db.QueryRow(
		"SELECT COALESCE(SUM(count), 0) FROM rate_limits WHERE agent_id = ? AND action = ? AND window_start > datetime('now', '-1 hour')",
		agentID, action,
	).Scan(&count)
	
	if err != nil {
		return false, err
	}
	return count < maxPerHour, nil
}

// IncrementRateLimit 在使用完毕功能之后对其特定代理做自增+1操作以维护计数限制状态。
func (d *DB) IncrementRateLimit(agentID, action string) error {
	// 用 UPSERT (ON CONFLICT) 原则无缝安全地进行频段记录初始化插入及加数累计
	_, err := d.db.Exec(`
		INSERT INTO rate_limits (agent_id, action, window_start, count)
		VALUES (?, ?, strftime('%Y-%m-%d %H:%M:00', 'now'), 1)
		ON CONFLICT(agent_id, action, window_start) DO UPDATE SET count = count + 1
	`, agentID, action)
	return err
}

// CleanupRateLimits 提供定点或者批量的计划任务方式删除超出其 2 个小时以前产生的过时计算条目，保持库文件的高效清理状态。
func (d *DB) CleanupRateLimits() error {
	_, err := d.db.Exec("DELETE FROM rate_limits WHERE window_start < datetime('now', '-2 hours')")
	return err
}
