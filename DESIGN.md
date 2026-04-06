# Design Document — cogvault MVP

스펙: `SPEC.md` (draft-5 final) 참조.
이 문서는 스펙의 **실현 방법**을 다룬다.

---

## 1. 컴포넌트 의존 그래프

```
cmd/cogvault/main.go
    │
    ▼
┌─────────┐     ┌───────────┐
│ mcp/    │────▶│ storage/  │
│ server  │     │ fs        │
│ tools   │──┐  └───────────┘
└─────────┘  │  ┌───────────┐     ┌───────────┐
             ├─▶│ index/    │────▶│ adapter/  │
             │  │ sqlite    │     │ obsidian  │
             │  └───────────┘     └───────────┘
             │  ┌───────────┐
             └─▶│ adapter/  │
                │ obsidian  │
                └───────────┘

모든 패키지 ──▶ errors/
모든 패키지 ──▶ config/
```

단방향. 순환 없음.

**v0.2 확장 포인트**: `engine/` 패키지가 `storage` + `index` + `adapter` + `llm`을 조합하여 compile/query/lint 구현. `mcp/tools.go`와 `cmd/`에서 engine 호출. 현재 `mcp/tools.go`의 조합 로직(write-then-index 등)이 engine 추출 후보.

---

## 2. 컴포넌트별 설계

### 2.1 errors

sentinel error 패키지. 스펙 4절 참조.

에러 매핑: `mcp/tools.go`의 `mapError` 공용 함수. switch 문. MVP 에러 5개 수준이면 충분.

### 2.2 config

```go
type Config struct {
    WikiDir             string   `yaml:"wiki_dir"`
    DBPath              string   `yaml:"db_path"`
    Exclude             []string `yaml:"exclude"`
    ExcludeRead         []string `yaml:"exclude_read"`
    Adapter             string   `yaml:"adapter"`
    ConsistencyInterval int      `yaml:"consistency_interval"`
}
```

`AllExcluded()`: `exclude` 뒤에 `exclude_read`를 순서 보존 연결한 슬라이스 반환. 정규화/중복 제거 없음.
`SchemaPath()`: `filepath.Join(WikiDir, "_schema.md")`.

**책임 경계**: config는 경로 문자열의 안전성 + 최소 정책 제약 검증 (traversal, 절대경로, 빈 값, 허용 목록, wiki_dir 격리, db_path 파일성). 실제 파일시스템 상태와 권한은 storage에서 집행.

### 2.3 storage/fs

```go
type FSStorage struct {
    root string
    cfg  *config.Config
    mu   sync.Mutex
}
```

경로 파이프라인:

```
relPath → filepath.Clean → ".." 체크 → abs 생성 → Lstat 심볼릭 체크 → 메서드별 검증
```

List 반환: `ListEntry{Path, Name, IsDir}`. title/type은 MCP 핸들러가 `Index.GetMeta`로 보강.

### 2.4 adapter/obsidian

두 파일: `scanner.go` + `parser.go`.

MVP에서 `linkresolve.go`, `cache.go`는 없음. `ResolveLink`는 v0.3에서 Lint와 함께 도입.

**scanner.go** — Scan:

```
filepath.WalkDir(root)
  ├── 디렉토리: AllExcluded? → SkipDir
  ├── .md 파일 → fn(relPath)
  └── 기타 → 무시
```

**parser.go** — Parse:

```
확장자 체크 → .md 아니면 ErrNotMarkdown
  │
  ▼
github.com/adrg/frontmatter 로 파싱
  ├── 성공 → Frontmatter 채움
  └── 실패 → Frontmatter={}, 전체를 본문으로
  │
  ▼
Title: frontmatter["title"] > 첫 # heading > 파일명
  │
  ▼
정규식으로 추출:
  \[\[([^\]]+)\]\]  → 캡처 그룹에서 target 추출
  !\[\[([^\]]+)\]\] → 캡처 그룹에서 file 추출
  │
  ▼
후처리:
  "target|display" → "target"   (| 뒤 제거)
  "target#heading" → "target"   (# 뒤 제거)
  │
  ▼
Links: ["target1", "target2"]        ← 대괄호 없음
Attachments: ["file1", "file2"]      ← 대괄호 없음
  │
  ▼
Tags: frontmatter["tags"] + 본문 인라인 #태그
Dataview: ^(\w+)::\s*(.+)$ 매칭
Aliases: frontmatter["aliases"]
```

**includeContent 참고**: Ingest 워크플로우에서 `include_content=true`가 사실상 필수. 메타데이터만으로는 에이전트가 source 페이지를 작성할 정보가 부족. 스펙 `_schema.md`에도 `wiki_parse(path, include_content=true)`로 명시.

`includeContent=false`여도 파일 전체를 읽어야 링크/태그 추출 가능. 내부 파싱은 바이트 기반으로 처리하되, `Source.Content`는 MCP JSON 계약에 맞춰 문자열로 직렬화하고 `includeContent=false`면 필드를 생략한다.

### 2.5 index/sqlite

```go
type SQLiteIndex struct {
    db              *sql.DB
    cfg             *config.Config
    lastConsistency time.Time
    mu              sync.Mutex
}
```

**DB 초기화**:

```sql
PRAGMA journal_mode=WAL;

CREATE VIRTUAL TABLE IF NOT EXISTS wiki_fts USING fts5(
    path, title, content, tags,
    tokenize='trigram'
);

CREATE TABLE IF NOT EXISTS file_meta (
    path TEXT PRIMARY KEY,
    title TEXT DEFAULT '',
    type TEXT DEFAULT '',
    content_hash TEXT NOT NULL,
    mod_time TEXT NOT NULL,
    indexed_at TEXT NOT NULL
);
```

**Add**: 단일 SQL 트랜잭션으로 FTS + file_meta 동시 갱신.

**Search**:
- query 길이 ≥ 3 → FTS5 MATCH
- query 길이 ≤ 2 → `content LIKE '%query%'`
- scope: `WHERE path LIKE '{wikiDir}/%'` 또는 `NOT LIKE`. `wikiDir`은 `cfg.WikiDir`.
- `snippet`: 첫 일치 지점 주변 텍스트를 잘라 생성. 추출 불가 시 빈 문자열.
- `score`: 정렬용 opaque 값. FTS5 MATCH와 LIKE fallback은 서로 다른 계산식을 쓸 수 있으며, 같은 응답 안에서 내림차순 정렬만 보장.

**CheckConsistency(storage, adapter, force)**:

```
force=false AND now-lastConsistency < interval → skip
  │
  ▼
1. file_meta 전체 (path, mod_time) 로드
2. 각 path: os.Stat → 삭제/변경 → Remove 또는 재인덱싱
3. adapter.Scan → file_meta에 없는 신규 → adapter.Parse + Add
4. lastConsistency = now
```

**GetMeta**: `SELECT ... FROM file_meta WHERE path = ?`. 미존재 → `ErrNotFound`.

### 2.6 mcp/

**server.go**:

```go
func NewServer(cfg, store, idx, adpt) *server.MCPServer {
    s := server.NewMCPServer("cogvault", "0.1.0",
        server.WithInstructions(schemaInstructions(cfg, store)),
    )
    registerTools(s, cfg, store, idx, adpt)
    return s
}
```

**instructions 전략**:
- `cfg.SchemaPath()` 전문 읽기.
- 2,000자 이하: 전문 포함.
- 2,000자 초과: 앞 2,000자 + `fmt.Sprintf("\n\n[전문은 wiki_read(%q)로 확인]", cfg.SchemaPath())`.
- 읽기 실패: 하드코딩된 기본 요약.
- 섹션 추출 로직 불필요. 단순 절삭.

**tools.go 핸들러 패턴**:

모든 핸들러는 `registerTools`에서 클로저로 `store`, `idx`, `adpt`를 캡처. 구조체 필드가 아닌 함수 클로저로 의존성 전달.

1. 파라미터 추출 + 검증
2. storage/index/adapter 호출
3. `mapError` → MCP 에러
4. JSON 직렬화

**write-then-index** (write 핸들러):

```go
// NOTE: v0.2에서 engine/service 레이어로 추출 후보.
func handleWrite(cfg, store, idx, adpt) handler {
    return func(ctx, req) {
        path, content := extractArgs(req)

        if err := store.Write(path, []byte(content)); err != nil {
            return mapError(err)
        }

        // best-effort 인덱싱
        if strings.HasSuffix(path, ".md") {
            if src, err := adpt.Parse(root, path, false); err == nil {
                _ = idx.Add(path, content, extractMeta(src))
            }
        }

        return writeResponse(path, len(content), nil)
    }
}
```

**listWithMeta** (list 핸들러 내부 헬퍼):

```go
// NOTE: v0.2에서 engine/service 레이어로 추출 후보.
func listWithMeta(store, idx, adpt, prefix) ([]map[string]any, error) {
    if _, _, _, err := idx.CheckConsistency(store, adpt, false); err != nil {
        return nil, err
    }
    entries, err := store.List(prefix)
    if err != nil { return nil, err }

    results := make([]map[string]any, len(entries))
    for i, e := range entries {
        r := map[string]any{
            "path": e.Path, "name": e.Name, "is_dir": e.IsDir,
            "title": "", "type": "",
        }
        if !e.IsDir {
            if meta, err := idx.GetMeta(e.Path); err == nil {
                r["title"] = meta.Title
                r["type"] = meta.Type
            }
        }
        results[i] = r
    }
    return results, nil
}
```

`map[string]any`로 JSON 직접 구성. 별도 ListResult 타입 불필요.

---

## 3. 데이터 흐름

### 3.1 init

```
parseFlags → config 로드/생성 → wiki_dir/ 생성 → SchemaPath() 복사(embed)
  → DB 생성 → CheckConsistency(force=true) → 출력
```

### 3.2 serve

```
parseFlags → config.Load → 검증 (실패 시 exit 1)
  → storage/index/adapter 생성
  → CheckConsistency(force=true)
  → mcp.NewServer → server.ServeStdio (블로킹)
  → cleanup
```

### 3.3 에이전트 Ingest (passthrough, 6회 호출)

```
에이전트                          MCP 서버
  │  (instructions로 스키마 수신)
  ├─ wiki_scan("notes/")     ──▶ Scan → 경로 목록
  ├─ wiki_parse(path,true)   ──▶ Parse(includeContent=true) → Source
  │  (내용 분석)
  ├─ wiki_search(q,"wiki")   ──▶ CheckConsistency(false) + FTS5 → Results
  ├─ wiki_write(source_page) ──▶ Write + best-effort Add → 성공
  ├─ wiki_read(related_page) ──▶ Read → 내용
  └─ wiki_write(updated)     ──▶ Write + best-effort Add → 성공
```

---

## 4. 설계 결정

### 4.1 eventual consistency

Write-then-index + CheckConsistency. 단순. 부분 실패 허용.

### 4.2 trigram 토크나이저

Pure Go SQLite에서 ICU 불확실. trigram은 추가 의존 없이 한국어 동작. 2글자 이하 LIKE fallback.

### 4.3 Storage/Index 분리

독립 mock, 검색 엔진 교체 가능. 비용: 조합 로직이 mcp/에 위치 → v0.2에서 engine으로.

### 4.4 `//go:embed`로 default_schema.md 배포

```go
//go:embed schema/default_schema.md
var defaultSchema string
```

싱글 바이너리. init 시 `_schema.md` 없으면 이 내용으로 생성.

---

## 5. 파일별 책임

| 파일 | 책임 |
|------|------|
| `errors/errors.go` | sentinel error |
| `config/config.go` | YAML, 기본값, 검증 |
| `storage/storage.go` | 인터페이스 + ListEntry |
| `storage/fs.go` | 파일시스템, 보안, 뮤텍스 |
| `adapter/adapter.go` | 인터페이스 + Source |
| `adapter/obsidian/scanner.go` | WalkDir Scan |
| `adapter/obsidian/parser.go` | frontmatter, wikilink, tag, dataview |
| `adapter/markdown/parser.go` | 표준 마크다운 fallback |
| `index/index.go` | 인터페이스 + Result + FileMeta |
| `index/sqlite.go` | FTS5, file_meta, CheckConsistency, GetMeta |
| `mcp/server.go` | MCP 서버, instructions |
| `mcp/tools.go` | 도구 6개, mapError, listWithMeta |
| `cmd/cogvault/main.go` | cobra CLI |
| `schema/default_schema.md` | go:embed 대상 |

---

## 6. 동시성

```
Storage.Read     — 잠금 없음
Storage.Write    — Storage.mu
Index.Search     — SQLite WAL 읽기
Index.Add        — Index.mu
Index.GetMeta    — SQLite WAL 읽기
CheckConsistency — Index.mu
```

Storage.mu ↔ Index.mu 동시 미획득. 데드락 없음.

---

## 7. 테스트 설계

| 대상 | 방법 |
|------|------|
| config | YAML 생성 → Load → 검증 |
| storage/fs | `t.TempDir()` + fixtures |
| adapter/obsidian | fixtures/obsidian, edge |
| index/sqlite | 임시 DB. force=true/false 분기 |
| MCP 도구 | mcp-go 테스트 클라이언트 |
| 통합 | init→write→search round-trip |
| 레이스 | `go test -race ./...` |

---

## 8. 구현 순서

```
Step 1: errors + config
Step 2: storage (인터페이스 + fs + 보안 테스트)
Step 3: adapter (인터페이스 + obsidian scanner/parser + 파싱 테스트)
Step 4: index (인터페이스 + sqlite + GetMeta + 정합성 테스트)
Step 5: mcp (server + tools + round-trip 테스트)
Step 6: cmd (cobra: init/search/serve + CLI 테스트)
Step 7: schema (default_schema.md + go:embed + instructions)
Step 8: testdata/fixtures/real + 통합 테스트
Step 9: 1주 실사용 (스펙 11절)
```
