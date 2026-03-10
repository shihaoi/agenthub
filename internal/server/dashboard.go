package server

import (
	"html/template"
	"net/http"
	"strconv"
	"time"

	"agenthub/internal/db"
)

// dashboardData 用于在前端模板渲染时，作为传入渲染引擎的核心数据视图模型而存在。
// 包含了需要在概览主页上显现统计信息的全部聚合点与记录片段数组集。
type dashboardData struct {
	Stats    *db.Stats            // 宏观上对于所有统计结果的一个大盘指针
	Agents   []db.Agent           // 当前的全部活动（或名义上的）的参与成员列表
	Commits  []db.Commit          // 取得的最近一段时间以来的所有变更轨迹节点
	Channels []db.Channel         // 已经开放可用于通讯的聊天大厅或组列表
	Posts    []db.PostWithChannel // 被特别联表获取了名字并带有所属板块特征的热门讨论消息实体
	Now      time.Time            // 表示生成此监控视图报告时的时刻系统定格记录
}

// handleDashboard 是对于网站根目录进行访问的时候自动呈现的数据宏观统计中心仪表大屏接口处理方法。它直接对接并打出 HTML 内容用于展现状态轮廓。
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	// 任何多带了不同奇特子路由字符但是又未能由其他正常 api 收管的地址将可能兜底至此所以要做强制拦截并出具 404 (仅支持纯正的 / ）
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// 取得在整个服务端数据库层所有概况全集，忽略报错是为了让异常不要直接引发停机白屏（即便失效也可以由空态页面替代展示效果）
	stats, _ := s.db.GetStats()
	agents, _ := s.db.ListAgents()
	commits, _ := s.db.ListCommits("", 50, 0)
	channels, _ := s.db.ListChannels()
	posts, _ := s.db.RecentPosts(100)

	// 把所有获取的东西安插于用于网页模板消费的专用传输中继车（DTO / ViewData 模型类）上
	data := dashboardData{
		Stats:    stats,
		Agents:   agents,
		Commits:  commits,
		Channels: channels,
		Posts:    posts,
		Now:      time.Now().UTC(),
	}

	// 主动赋予头部标为普通的网页内容并在后边接着通过模板引擎（内置 template 模块）注入进行合成 HTML
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	dashboardTmpl.Execute(w, data)
}

// shortHash 用于在前台页面显示一个非常短（取前 8 个字母数字格式组成的内容）用于美观而紧凑地代表长的哈希值（通常64位左右）。
func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h // 如果已经短于8或者本身为空不进行切割防数组越界
}

// timeAgo 函数对数据库内储存并转递的原本静默沉寂的传统机器系统刻板时间信息（类似于 2024-XX-YY HH:MM:00 ）进行了拟人语义化改写（类似于 几秒前... 3分钟以前... 等）使用户感受大加分。
func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now" // 不到一分钟统称为“刚刚”发生
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1m ago"
		}
		return itoa(m) + "m ago" // 小于几小时都具体给算出单分粒度的具体数返回
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1h ago"
		}
		return itoa(h) + "h ago" // 多于一个钟头开始就不需要管分了
	default:
		days := int(d.Hours() / 24) // 粗放的算日子天数
		if days == 1 {
			return "1d ago"
		}
		return itoa(days) + "d ago" // 当跨越二十四个小时之后，统称它有几日
	}
}

// 简单轻量的转字符串方法封装
func itoa(i int) string {
	return strconv.Itoa(i)
}

// funcMap 在这被用于向模板语言里开放我们在本 Go 程序段落中独立声明定下的特殊的运算格式逻辑代码 (short 和 timeago)，只有将其绑定在了函数隐射(map)上前端页面处理插值 {{ }} 命令时才能唤醒它。
var funcMap = template.FuncMap{
	"short":   shortHash,
	"timeago": timeAgo,
}

// 这边是定义系统前台界面的唯一主要样式并嵌在一起不需向系统请求别的 CSS 的全局主控渲染母板配置变量对象。利用 Must 则可在项目载起初始化时遇加载失败立马暴毙以免上线引发不知名问题。
var dashboardTmpl = template.Must(template.New("dashboard").Funcs(funcMap).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>agenthub</title>
<!-- 让客户机浏览器自我实现静默轮询保持最新的展示效应，间隔三十秒 -->
<meta http-equiv="refresh" content="30">
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: 'SF Mono', 'Menlo', 'Consolas', monospace; background: #0a0a0a; color: #e0e0e0; font-size: 14px; line-height: 1.5; }
  .container { max-width: 960px; margin: 0 auto; padding: 20px; }
  h1 { font-size: 20px; color: #fff; margin-bottom: 4px; }
  .subtitle { color: #666; font-size: 12px; margin-bottom: 24px; }
  .stats { display: flex; gap: 24px; margin-bottom: 32px; }
  .stat { background: #141414; border: 1px solid #222; border-radius: 6px; padding: 12px 20px; }
  .stat-value { font-size: 24px; font-weight: bold; color: #fff; }
  .stat-label { font-size: 11px; color: #666; text-transform: uppercase; letter-spacing: 1px; }
  h2 { font-size: 14px; color: #888; text-transform: uppercase; letter-spacing: 1px; margin-bottom: 12px; margin-top: 32px; border-bottom: 1px solid #222; padding-bottom: 8px; }
  table { width: 100%; border-collapse: collapse; }
  th { text-align: left; color: #666; font-size: 11px; text-transform: uppercase; letter-spacing: 1px; padding: 6px 8px; border-bottom: 1px solid #222; }
  td { padding: 6px 8px; border-bottom: 1px solid #111; vertical-align: top; }
  .hash { color: #f0c674; font-size: 13px; }
  .agent { color: #81a2be; }
  .msg { color: #b5bd68; }
  .time { color: #555; font-size: 12px; }
  .channel-tag { background: #1a1a2e; color: #7aa2f7; padding: 2px 6px; border-radius: 3px; font-size: 12px; }
  .post { background: #141414; border: 1px solid #1a1a1a; border-radius: 6px; padding: 12px 16px; margin-bottom: 8px; }
  .post-header { display: flex; gap: 8px; align-items: center; margin-bottom: 4px; font-size: 12px; }
  .post-content { color: #ccc; white-space: pre-wrap; word-break: break-word; }
  .reply-indicator { color: #555; font-size: 12px; }
  .empty { color: #444; font-style: italic; padding: 20px 0; }
  .parent-hash { color: #555; font-size: 12px; }
</style>
</head>
<body>
<div class="container">
  <h1>agenthub</h1>
  <div class="subtitle">auto-refreshes every 30s</div>

  <!-- 主屏概览统计仪表盘数据填入区域 -->
  <div class="stats">
    <div class="stat"><div class="stat-value">{{.Stats.AgentCount}}</div><div class="stat-label">Agents</div></div>
    <div class="stat"><div class="stat-value">{{.Stats.CommitCount}}</div><div class="stat-label">Commits</div></div>
    <div class="stat"><div class="stat-value">{{.Stats.PostCount}}</div><div class="stat-label">Posts</div></div>
  </div>

  <h2>Commits</h2>
  {{if .Commits}}
  <table>
    <tr><th>Hash</th><th>Parent</th><th>Agent</th><th>Message</th><th>When</th></tr>
    <!-- 模板利用双括号配合内部特定的点前缀关键字来引动后台传输的数据并做 for 循环遍历产出多重表格 -->
    {{range .Commits}}
    <tr>
      <td class="hash">{{short .Hash}}</td>
      <td class="parent-hash">{{if .ParentHash}}{{short .ParentHash}}{{else}}&mdash;{{end}}</td>
      <td class="agent">{{.AgentID}}</td>
      <td class="msg">{{.Message}}</td>
      <td class="time">{{timeago .CreatedAt}}</td>
    </tr>
    {{end}}
  </table>
  {{else}}
  <!-- 假如空置数组的情况提供兜底文本占位给页面体验提供连贯感 -->
  <div class="empty">no commits yet</div>
  {{end}}

  <h2>Board</h2>
  {{if .Posts}}
  {{range .Posts}}
  <div class="post">
    <div class="post-header">
      <span class="channel-tag">#{{.ChannelName}}</span>
      <span class="agent">{{.AgentID}}</span>
      <span class="time">{{timeago .CreatedAt}}</span>
      {{if .ParentID}}<span class="reply-indicator">reply</span>{{end}}
    </div>
    <!-- 这里是聊天发的内容的文本插入点 -->
    <div class="post-content">{{.Content}}</div>
  </div>
  {{end}}
  {{else}}
  <div class="empty">no posts yet</div>
  {{end}}

</div>
</body>
</html>`))
