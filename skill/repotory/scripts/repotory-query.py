#!/usr/bin/env python3
"""Cinder 兜底只读查询工具（通道 C）。

当 MCP server 不可用、headless server 也没在跑时，用它直接查本地 SQLite。

严格只读：只发 SELECT，不 INSERT/UPDATE/DELETE，不写库文件。
要写知识请回到通道 A（MCP 工具）或通道 B（HTTP RPC）——
直接改库会绕过 handoff 保护、主题归属校验与版本历史。

用法：
    repotory-query.sh "搜索词"          FTS 全文检索
    repotory-query.sh --topics          列出全部主题
    repotory-query.sh --projects        列出全部项目
    repotory-query.sh --note <id>       读单条笔记全文
    repotory-query.sh --stats           库概况（笔记数 / 主题数 / 索引状态）
"""

import os
import re
import sqlite3
import sys

# 与 internal/db/search.go 的 searchStopwords 保持一致：仅用于放松 OR 检索时
# 剔除纯功能词，绝不用来拒绝严格 AND 匹配。
STOPWORDS = {
    "the", "and", "for", "with", "that", "this", "from", "they", "have",
    "were", "what", "when", "your", "about", "into", "there", "these",
    "some", "then", "than",
}

# 与 internal/db/search.go 的 maxFTSORTerms 同量级，避免超长查询拖垮扫描。
MAX_FTS_TERMS = 32


def db_path():
    """按平台定位库文件；可用 REPOTORY_DB 覆盖。"""
    env = os.environ.get("REPOTORY_DB")
    if env:
        return env
    if sys.platform == "darwin":
        base = os.path.expanduser("~/Library/Application Support/repotory")
    elif sys.platform.startswith("win"):
        base = os.environ.get("APPDATA", os.path.expanduser("~"))
        base = os.path.join(base, "repotory")
    else:
        base = os.environ.get("XDG_CONFIG_HOME", os.path.expanduser("~/.config"))
        base = os.path.join(base, "repotory")
    return os.path.join(base, "repotory.db")


def is_cjk_run(text):
    """整串都是 CJK 字符才算 CJK run —— 混排词按普通词处理（与生产一致）。"""
    if not text:
        return False
    for ch in text:
        cp = ord(ch)
        if not (
            0x4E00 <= cp <= 0x9FFF
            or 0x3400 <= cp <= 0x4DBF
            or 0x3040 <= cp <= 0x30FF
            or 0xAC00 <= cp <= 0xD7AF
        ):
            return False
    return True


def expand_for_fts(term):
    """CJK 串切成重叠 trigram；非 CJK 原样返回。"""
    if not is_cjk_run(term):
        return [term]
    runes = list(term)
    if len(runes) < 3:
        return [term]
    return ["".join(runes[i:i + 3]) for i in range(len(runes) - 2)]


def build_match(query):
    """构造 MATCH 表达式。

    严格模式：每个词组加引号后空格相连 = AND 语义。
    词太少（<2 个可匹配词）时放宽为 OR —— 单个词的 AND 与 OR 等价，
    放宽只会引入噪音，得不偿失，所以生产代码在这种情况下返回空。
    """
    terms = []
    for raw in query.split():
        for t in expand_for_fts(raw):
            t = t.replace('"', '""')
            if t:
                terms.append(f'"{t}"')
    if not terms:
        return None, "empty"
    if len(terms) == 1:
        return terms[0], "single"

    # 放松：去掉英文停用词与 <3 字符的 ASCII 词（trigram 分词器匹配不到）
    relaxed = []
    for t in terms:
        plain = t.strip('"').lower()
        if plain in STOPWORDS:
            continue
        if not is_cjk_run(plain) and len(plain) < 3:
            continue
        relaxed.append(t)
        if len(relaxed) >= MAX_FTS_TERMS:
            break

    if len(relaxed) >= 2:
        return " OR ".join(relaxed), "or"
    return " ".join(terms), "and"


def connect(path):
    if not os.path.exists(path):
        sys.exit(f"error: 数据库不存在: {path}\n提示：先装 Cinder 并跑一次扫描，或设置 REPOTORY_DB 指向库文件。")
    # 只读 URI：WAL 模式下桌面应用开着也能读，且这里杜绝任何写入意图。
    conn = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row
    return conn


def has_table(conn, name):
    row = conn.execute(
        "SELECT 1 FROM sqlite_master WHERE type IN ('table','view') AND name = ?", (name,)
    ).fetchone()
    return row is not None


def cmd_search(conn, query, limit=10):
    if not has_table(conn, "project_notes_fts"):
        sys.exit("error: 找不到 FTS 索引表 project_notes_fts —— 库可能未初始化或 schema 过旧。")

    expr, mode = build_match(query)
    if expr is None:
        print("error: 查询为空。")
        return

    sql = (
        "SELECT n.id, n.project_id, n.title, "
        "       substr(replace(replace(n.content, char(10), ' '), char(13), ' '), 1, 200) AS snippet, "
        "       bm25(project_notes_fts) AS rank, "
        "       COALESCE(t.name, '') AS topic_name "
        "FROM project_notes_fts f "
        "JOIN project_notes n ON n.id = f.rowid "
        "LEFT JOIN note_topics t ON t.id = n.topic_id "
        "WHERE project_notes_fts MATCH ? "
        "ORDER BY bm25(project_notes_fts), n.pinned DESC, n.updated_at DESC "
        "LIMIT ?"
    )
    try:
        rows = conn.execute(sql, (expr, limit)).fetchall()
    except sqlite3.OperationalError as e:
        rows = []
        fts_error = str(e)

    # The FTS5 trigram tokenizer cannot match a term shorter than 3
    # characters, so a two-character Chinese query legitimately returns
    # nothing even when the note exists. Production handles this with a LIKE
    # fallback (searchNotesLike); this mirrors it, and only ever runs on an
    # EMPTY FTS result so it cannot displace a working answer.
    fallback_note = ""
    if not rows:
        like_rows, like_error = search_like(conn, query, limit)
        if like_rows:
            rows = like_rows
            mode = "like"
            fallback_note = "（短查询走了 LIKE 兜底：trigram 匹配不到 <3 字符的词）"
        elif like_error:
            print(f"error: FTS 查询失败 ({fts_error})；LIKE 兜底也失败 ({like_error})")
            return

    print(f'查询: "{query}"   匹配模式: {mode}   命中: {len(rows)}')
    if fallback_note:
        print(fallback_note)
    print("-" * 72)
    for r in rows:
        print(f"[{r['id']}] project={r['project_id']}  {r['title']}")
        if r["topic_name"]:
            print(f"        主题: {r['topic_name']}")
        snippet = " ".join((r["snippet"] or "").split())
        if snippet:
            print(f"        {snippet}")
    if not rows:
        print("无命中。可能是真的没有；也可能 FTS 索引漂移 —— 走通道 A 跑 repotory_integrity 确认。")


def like_terms(query):
    """把查询切成值得 OR 的单元：按空白切，并剥掉只会妨碍匹配的标点。

    与生产 likeTerms 同构 —— `/health` 能被找到，`/health,` 不能。
    """
    units = []
    seen = set()
    for raw in query.split():
        f = raw.strip("".join(
            chr(c) for c in range(0x21)  # control + space
        ) + "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~")
        if not f or f in seen:
            continue
        seen.add(f)
        units.append(f)
    return units


def search_like(conn, query, limit):
    """LIKE 兜底：FTS5 trigram 匹配不到 <3 字符的词时使用。

    先用整串子串匹配（一条包含整个查询的笔记最相关）；零命中再按词 OR。
    排名：标题命中 > 正文命中，置顶加权，最近更新优先 —— 与生产一致。
    """
    fields = "n.title LIKE ? ESCAPE '\\' OR n.content LIKE ? ESCAPE '\\' OR n.tags LIKE ? ESCAPE '\\'"
    base = (
        "SELECT n.id, n.project_id, n.title, "
        "       substr(replace(replace(n.content, char(10), ' '), char(13), ' '), 1, 200) AS snippet, "
        "       0 AS rank, "
        "       COALESCE(t.name, '') AS topic_name, "
        "       CASE WHEN n.title LIKE ? ESCAPE '\\' THEN 1 ELSE 0 END AS title_hit "
        "FROM project_notes n "
        "LEFT JOIN note_topics t ON t.id = n.topic_id "
    )
    tail = " ORDER BY title_hit DESC, n.pinned DESC, n.updated_at DESC LIMIT ?"

    def esc(s):
        return s.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")

    attempts = [[query]]
    terms = like_terms(query)
    if len(terms) >= 2:
        attempts.append(terms)

    for attempt in attempts:
        if len(attempt) == 1:
            clause = fields
            params = [f"%{esc(attempt[0])}%"] * 3
        else:
            clause = "(" + " OR ".join(fields for _ in attempt) + ")"
            params = []
            for t in attempt:
                params.extend([f"%{esc(t)}%"] * 3)
        # title_hit 需要单独的参数组（它在 SELECT 里）。
        title_params = [f"%{esc(attempt[0])}%"] if len(attempt) == 1 else [
            f"%{esc(t)}%" for t in attempt
        ]
        title_clause = " OR ".join(
            "n.title LIKE ? ESCAPE '\\'" for _ in title_params
        )
        sql = base.replace(
            "CASE WHEN n.title LIKE ? ESCAPE '\\' THEN 1 ELSE 0 END AS title_hit",
            f"CASE WHEN {title_clause} THEN 1 ELSE 0 END AS title_hit",
        ) + f" WHERE {clause}" + tail
        try:
            rows = conn.execute(sql, title_params + params + [limit]).fetchall()
        except sqlite3.OperationalError as e:
            return [], str(e)
        if rows:
            return rows, ""
    return [], ""


def cmd_topics(conn):
    if not has_table(conn, "note_topics"):
        print("(无主题层 schema)")
        return
    rows = conn.execute(
        "SELECT t.id, t.project_id, t.name, "
        "       (SELECT COUNT(*) FROM project_notes n WHERE n.topic_id = t.id) AS note_count "
        "FROM note_topics t ORDER BY t.project_id, t.name"
    ).fetchall()
    print(f"主题（project → topic → note）共 {len(rows)} 个")
    print("-" * 72)
    for r in rows:
        print(f"[{r['id']}] project={r['project_id']}  {r['name']}  ({r['note_count']} 条笔记)")
    if not rows:
        print("（还没有主题。主题由人在桌面端创建 —— agent 不能建/改/删主题。）")


def cmd_projects(conn):
    rows = conn.execute(
        "SELECT p.id, p.name, p.root_path, "
        "       (SELECT COUNT(*) FROM project_notes n WHERE n.project_id = p.id) AS notes "
        "FROM projects p ORDER BY p.name"
    ).fetchall()
    print(f"项目共 {len(rows)} 个")
    print("-" * 72)
    for r in rows:
        path = (r["root_path"] or "")[:60]
        print(f"[{r['id']}] {r['name']}  ({r['notes']} 条笔记)  {path}")


def cmd_note(conn, note_id):
    row = conn.execute(
        "SELECT n.id, n.project_id, n.title, n.content, n.kind, n.tags, n.created_at, n.updated_at, "
        "       COALESCE(t.name, '') AS topic_name "
        "FROM project_notes n LEFT JOIN note_topics t ON t.id = n.topic_id "
        "WHERE n.id = ?",
        (note_id,),
    ).fetchone()
    if not row:
        print(f"error: 没有 id={note_id} 的笔记。")
        return
    print(f"# {row['title']}   [id={row['id']}]")
    print(f"project={row['project_id']}  kind={row['kind']}  tags={row['tags']}")
    if row["topic_name"]:
        print(f"主题: {row['topic_name']}")
    print(f"创建: {row['created_at']}   更新: {row['updated_at']}")
    print("-" * 72)
    print(row["content"] or "")


def cmd_stats(conn):
    total = conn.execute("SELECT COUNT(*) AS c FROM project_notes").fetchone()["c"]
    tagged = conn.execute(
        "SELECT COUNT(*) AS c FROM project_notes WHERE topic_id IS NOT NULL AND topic_id != 0"
    ).fetchone()["c"]
    topics = 0
    if has_table(conn, "note_topics"):
        topics = conn.execute("SELECT COUNT(*) AS c FROM note_topics").fetchone()["c"]
    projects = conn.execute("SELECT COUNT(*) AS c FROM projects").fetchone()["c"]
    todos = conn.execute("SELECT COUNT(*) AS c FROM project_todos").fetchone()["c"]

    print("库概况")
    print("-" * 72)
    print(f"  项目: {projects}    笔记: {total}    待办: {todos}")
    print(f"  主题: {topics}    已归类笔记: {tagged}")
    print(f"  库路径: {db_path()}")

    if has_table(conn, "project_notes_fts"):
        idx = conn.execute("SELECT COUNT(*) AS c FROM project_notes_fts").fetchone()["c"]
        print(f"  FTS 索引行: {idx}", end="")
        if idx != total:
            print(f"  ← 与笔记数（{total}）不一致，索引可能漂移（走通道 A 跑 repotory_integrity）")
        else:
            print("  ✓")
    else:
        print("  FTS 索引表不存在 —— 库可能未初始化。")


def main():
    args = sys.argv[1:]
    if not args:
        print(__doc__)
        return

    path = db_path()
    conn = connect(path)

    try:
        if args[0] == "--topics":
            cmd_topics(conn)
        elif args[0] == "--projects":
            cmd_projects(conn)
        elif args[0] == "--stats":
            cmd_stats(conn)
        elif args[0] == "--note":
            if len(args) < 2:
                sys.exit("error: --note 需要一个 id。")
            cmd_note(conn, int(args[1]))
        elif args[0].startswith("-"):
            sys.exit(f"error: 未知参数 {args[0]}\n{__doc__}")
        else:
            # 只把 --limit=N 形式的参数当选项；其余非选项词都是查询内容。
            limit = 10
            terms = []
            for a in args:
                m = re.match(r"^--limit=(\d+)$", a)
                if m:
                    limit = min(int(m.group(1)), 100)
                elif a.startswith("--"):
                    sys.exit(f"error: 未知参数 {a}\n{__doc__}")
                else:
                    terms.append(a)
            if not terms:
                sys.exit("error: 查询为空。")
            cmd_search(conn, " ".join(terms), limit)
    finally:
        conn.close()


if __name__ == "__main__":
    main()
