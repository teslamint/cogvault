# MVP Spec — cogvault

버전: draft-5 (final)
범위: MVP (v0.1) 만. v0.2 이후는 실사용 후 별도 스펙.
상태: **구현 준비 완료.** 구현 중 발견되는 이슈는 스펙 갱신으로 반영.

> **구현 시작 전 필수 사전 작업**:
> 1. 프로젝트명 확정 (GitHub 가용성 확인, Go 모듈 경로 유효성)
> 2. 자신의 Obsidian vault에서 민감 정보 제거한 테스트 fixture 준비
> 3. `mcp-go` 버전 선정 및 고정

---

## 1. 개요

### 1.1 목적

Obsidian vault를 LLM 에이전트가 읽고, 검색하고, 설정된 `wiki_dir/` 디렉토리에 구조화된 위키를 빌드할 수 있게 하는 **MCP 도구 서버 + CLI**.

### 1.2 MVP 범위

- MCP stdio 서버: 도구 6개 (읽기, 쓰기, 목록, 검색, 스캔, 파싱)
- CLI: `init`, `search`, `serve`
- Obsidian vault 하이브리드 통합
- SQLite FTS5 전문 검색
- Passthrough 모드 전용 (LLM 호출 없음)

### 1.3 MVP 범위 밖

- LLM 어댑터 (claudecode, anthropic, ollama)
- 자동 컴파일러 (engine/compiler)
- Query file-back, Lint
- `wiki_delete` 도구
- SSE 전송
- 벡터 검색, 온톨로지 그래프
- watch 모드 (fsnotify)
- `_log.md` 자동 관리
- 링크 해석 (`ResolveLink`) — v0.3에서 Lint와 함께 도입

---

## 2. 런타임 구조

### 2.1 Vault 레이아웃

기본 설정(`wiki_dir: "_wiki"`) 기준 예시:

```
obsidian-vault/
├── .obsidian/                 # 무시
├── .cogvault.yaml          # 설정 파일
├── .cogvault.db            # SQLite DB
├── _wiki/                     # LLM 소유 영역
│   ├── _schema.md             # 규칙 정의 (읽기 전용)
│   ├── entities/
│   ├── concepts/
│   ├── sources/
│   └── synthesis/
└── (기존 vault 파일들)         # raw source
```

### 2.2 접근 규칙

| 영역 | 읽기 | 쓰기 |
|------|------|------|
| vault 일반 파일 | 허용 (`exclude_read` 제외) | **거부** |
| `wiki_dir/` 내부 (일반) | 허용 | 허용 |
| `wiki_dir/_schema.md` | 허용 | **거부** (사용자 직접 수정만) |
| `exclude_read` 디렉토리 | **거부** | **거부** |
| `.obsidian/`, `.trash/` | 허용 (`exclude_read` 제외), 단 스캔/인덱싱/목록 제외 | **거부** |

### 2.3 인덱스

`wiki_list` (file_meta 캐시) + `wiki_search` (FTS5)가 담당. `_index.md` 없음.

### 2.4 스키마 전달

MCP 서버가 스키마 **요약**을 서버 instructions로 전달. 전문은 `wiki_read("<wiki_dir>/_schema.md")`로 조회한다. 기본 설정에서는 `wiki_read("_wiki/_schema.md")`.

---

## 3. 설정 파일

파일명: `.cogvault.yaml`, 위치: vault root

### 3.1 스키마

```yaml
wiki_dir: string        # 기본값: "_wiki"
db_path: string         # 기본값: ".cogvault.db"
exclude: string[]       # 스캔 + 인덱싱 제외. 기본값: [".obsidian", ".trash"]
exclude_read: string[]  # 읽기 + 스캔 + 인덱싱 전부 제외. 기본값: []
adapter: string         # 기본값: "obsidian". 허용: "obsidian", "markdown"
consistency_interval: int  # 정합성 체크 최소 간격 (초). 기본값: 5
```

### 3.2 `exclude`와 `exclude_read`의 관계

**`exclude_read`는 `exclude`를 암묵적으로 포함.**

- **스캔 + 인덱싱 제외** = `exclude` ∪ `exclude_read`
- **읽기 제외** = `exclude_read`
- **쓰기 제한** = `wiki_dir/` 외부 전체 + `wiki_dir/_schema.md`

### 3.3 경로 탐색

- `--vault` 지정 시: 해당 경로에서 탐색.
- `--vault` 미지정 시: 현재 디렉토리. 없으면 에러. 상위 탐색 안 함.

### 3.4 검증

- `wiki_dir`이 vault root 외부면 에러. `"."`(vault root 자체)도 거부.
- `db_path`: 절대경로, `..` 포함, `"."`, 경로 구분자로 끝나는 값 거부.
- `exclude`, `exclude_read`에 `..` 포함 시 에러. 절대경로, 빈 문자열, `"."` 거부.
- `adapter`가 허용 목록 외면 에러.
- 알 수 없는 설정 키가 있으면 에러 (오타 조기 차단).

---

## 4. 에러 타입

| 에러 | 의미 |
|------|------|
| `ErrNotFound` | 파일/경로 없음, 또는 디렉토리가 아닌 경로에 디렉토리 연산 |
| `ErrPermission` | 접근 거부 (쓰기 보호, 읽기 제외) |
| `ErrTraversal` | `..` 포함 경로 |
| `ErrSymlink` | 심볼릭 링크 접근 거부 |
| `ErrNotMarkdown` | `.md`가 아닌 파일에 대한 파싱 시도 |

---

## 5. Storage

### 5.1 인터페이스

```
Read(path) → ([]byte, error)
Write(path, data []byte) → error
List(prefix) → ([]ListEntry, error)
Exists(path) → (bool, error)
```

### 5.2 경로 규칙

- vault root 기준 **상대경로**.
- 정규화 후 `..` → `ErrTraversal`.
- 심볼릭 링크 → `ErrSymlink`.

### 5.3 Read

- `exclude_read` → `ErrPermission`.
- 파일 없음 → `ErrNotFound`.

### 5.4 Write

- `wiki_dir` 하위 아니면 `ErrPermission`.
- `_schema.md` 경로면 `ErrPermission`.
- 중간 디렉토리 **자동 생성**.
- 기존 파일 **덮어쓰기**.
- 인덱스 반영: eventual consistency (5.7).

### 5.5 List

- `prefix`는 **디렉토리**여야 함. 파일 경로 → `ErrNotFound`.
- 직계 자식만 (비재귀). 디렉토리는 `/`로 끝남.
- `exclude`, `exclude_read` 제외. 빈 디렉토리면 빈 배열.

### 5.6 Exists

- `exclude_read` → 항상 `false`.

### 5.7 일관성 모델

**Eventual consistency.** 쓰기↔인덱스 간 일시적 불일치 허용. 자동 탐지·복구 보장.

### 5.8 동시성

- 동일 경로 Write 직렬화. last-write-wins.
- Read↔Write 동시 가능.

---

## 6. Index

### 6.1 인터페이스

```
Add(path, content string, meta map[string]string) → error
Search(query string, limit int, scope string) → ([]Result, error)
Remove(path) → error
Rebuild() → error
CheckConsistency(storage, adapter, force bool) → (added, removed, updated int, error)
GetMeta(path) → (*FileMeta, error)
```

`CheckConsistency`는 Storage와 Adapter에 의존. 신규 파일 시 `Adapter.Parse`로 title, type 추출.
`force=true`: 간격 무시, 즉시 실행.
`GetMeta`: file_meta 단건 조회. 미존재 시 `ErrNotFound`.

### 6.2 Result

```
Result { Path, Title, Type, Snippet string; Score float64 }
```

### 6.3 FileMeta

```
FileMeta { Path, Title, Type, ContentHash, ModTime, IndexedAt string }
```

### 6.4 데이터 테이블

```
wiki_fts(path, title, content, tags)
file_meta(path PK, title, type, content_hash, mod_time, indexed_at)
```

### 6.5 인덱싱 범위

vault 전체 `.md`. `exclude` + `exclude_read` 제외. `wiki_dir/` 포함.

### 6.6 검색 동작

`limit` 기본 10, 최대 100. 스코어 내림차순. 빈 결과 = 빈 배열.

### 6.7 검색 범위 (scope)

| 값 | 대상 |
|----|------|
| `"all"` (기본) | 전체 |
| `"wiki"` | `wiki_dir` 하위만 |
| `"vault"` | `wiki_dir` 하위 제외 |

`wiki_dir` 설정값 참조.

### 6.8 한국어 검색

한국어 지원 필수. 2글자 이하도 결과 반환. 내부 방식 자유. 호출 측에 투명.

### 6.9 정합성 (bounded staleness)

- `wiki_list`, `wiki_search` 반환 전 정합성 보장.
- `force=false`: `consistency_interval` (기본 5초) 이내면 스킵.
- `force=true`: 즉시 실행.
- 보장: 삭제 파일 제거, 변경 파일 재인덱싱, 신규 파일 추가. exclude 규칙 준수.
- 성능: 대형 vault에서 체감 없을 것.

### 6.10 Add meta

| 키 | 용도 |
|----|------|
| `title` | 제목 |
| `type` | 페이지 타입 |
| `tags` | 태그 (쉼표 구분) |

### 6.11 동시성

동시 읽기 허용. 쓰기 직렬화.

---

## 7. Adapter

### 7.1 인터페이스

```
Name() → string
Scan(root, exclude []string, fn func(path string) error) → error
Parse(root, relPath string, includeContent bool) → (*Source, error)
```

MVP에서 `ResolveLink`는 미제공. v0.3에서 Lint와 함께 도입.

### 7.2 Source

```
Source {
    Path           string
    Title          string               # frontmatter title > 첫 # heading > 파일명
    Content        string               # includeContent=true일 때만 포함. 아니면 필드 생략.
    Frontmatter    map[string]any
    Links          []string             # 대괄호 미포함 target 문자열
    Attachments    []string             # 대괄호 미포함 파일명 문자열
    Tags           []string
    DataviewFields map[string]string
    Aliases        []string
    SourceType     string               # "obsidian" | "markdown"
}
```

### 7.3 Scan

- 콜백 패턴: `fn(path)`.
- `fn` 에러 시 즉시 중단.
- `exclude` 건너뜀. `.md`만. 재귀.
- 파일 경로 전달 시 `ErrNotFound`.

### 7.4 Parse

- `.md`만. 비-`.md` → `ErrNotMarkdown`.
- frontmatter: 경량 라이브러리 사용. 파싱 실패 시 빈 frontmatter + 전체를 본문으로.
- Title: frontmatter `title` > 첫 `#` heading > 파일명.

### 7.5 Wikilink 파싱

**추출 규칙** — Links/Attachments에는 **대괄호 없는 target만** 저장:

| 문법 | Links/Attachments에 저장되는 값 |
|------|-------------------------------|
| `[[target]]` | Links: `"target"` |
| `[[target\|display]]` | Links: `"target"` |
| `[[target#heading]]` | Links: `"target"` |
| `![[file]]` | Attachments: `"file"` |
| `![[file\|size]]` | Attachments: `"file"` |

코드블록 내 wikilink: MVP에서 무시 안 함 (false positive 허용).

### 7.6 Markdown 어댑터 (fallback)

Obsidian 문법 미지원. 표준 마크다운 링크, frontmatter만 파싱.

---

## 8. MCP 도구

### 8.1 공통 규칙

- 전송: stdio.
- 서버명: `cogvault`, 버전: `0.1.0`.
- path: vault root 기준 상대경로.
- 스키마 요약: 서버 instructions 자동 전달.
- 에러 매핑:

| sentinel error | MCP 메시지 |
|----------------|-----------|
| `ErrNotFound` | `"not found: {path}"` |
| `ErrPermission` | `"access denied: {path}"` |
| `ErrTraversal` | `"invalid path: {path}"` |
| `ErrNotMarkdown` | `"not a markdown file: {path}"` |
| 기타 | `"internal error: {message}"` |

### 8.2 wiki_read

| 파라미터 | `path: string` (필수) |
|---------|---------------------|
| 반환 | 파일 내용 (텍스트) |
| 에러 | `ErrNotFound`, `ErrPermission`, `ErrTraversal`, `ErrSymlink` |

### 8.3 wiki_write

| 파라미터 | `path: string` (필수), `content: string` (필수) |
|---------|-----------------------------------------------|
| 반환 | `{ "status": "written", "path": "...", "bytes": N, "warnings": [] }` |
| 에러 | `ErrPermission`, `ErrTraversal`, `ErrSymlink` |

- `bytes` = Go `len(content)`.
- `warnings`: 문자열 배열. MVP에서 항상 빈 배열.
- 부수 효과: 인덱스 반영 (eventual consistency).

### 8.4 wiki_list

디렉토리 브라우징. 비재귀. 메타데이터.

| 파라미터 | `prefix: string` (선택, 기본 `""`) |
|---------|----------------------------------|
| 반환 | `[{ "path", "name", "is_dir", "title", "type" }]` |
| 에러 | `ErrNotFound` (파일 경로 또는 없는 디렉토리), `ErrTraversal` |

정합성: bounded staleness (6.9). title/type: `GetMeta` 캐시.

### 8.5 wiki_search

| 파라미터 | `query: string` (필수), `limit: int` (선택, 기본 10), `scope: string` (선택, 기본 `"all"`) |
|---------|----------------------------------------------------------------------------------------|
| 반환 | `[{ "path", "title", "type", "snippet", "score" }]` |
| 에러 | 빈 결과는 에러 아님 |

정합성: bounded staleness (6.9). scope: `wiki_dir` 참조 (6.7).
- `snippet`: 일치 지점 주변의 짧은 발췌. 추출할 수 없으면 빈 문자열.
- `score`: 정렬용 `float64`. 같은 응답 안에서의 상대 순서만 의미 있으며, 검색 방식(FTS5/LIKE fallback) 간 절대값 비교는 보장하지 않음.

### 8.6 wiki_scan

vault `.md` 경로 목록. 재귀. wiki_list와의 차이: 재귀 + 경로만.

| 파라미터 | `dir: string` (선택, 기본 `""`) |
|---------|-------------------------------|
| 반환 | 경로 문자열 배열 |
| 에러 | `ErrNotFound` (파일 경로 또는 없는 디렉토리) |

### 8.7 wiki_parse

`.md` 메타데이터. 선택적 본문.

| 파라미터 | `path: string` (필수), `include_content: bool` (선택, 기본 `false`) |
|---------|------------------------------------------------------------------|
| 반환 | Source (JSON) |
| 에러 | `ErrNotFound`, `ErrPermission`, `ErrNotMarkdown` |

반환 예 (`include_content=false`):

```json
{
  "path": "notes/idea.md",
  "title": "새로운 아이디어",
  "frontmatter": {"tags": ["project", "ai"]},
  "links": ["related-note", "another"],
  "attachments": ["diagram.png"],
  "tags": ["project", "ai"],
  "dataview_fields": {"status": "draft"},
  "aliases": ["idea-v2"],
  "source_type": "obsidian"
}
```

`include_content=true`면 `"content": "..."` 필드 추가.

---

## 9. CLI

### 9.1 init

```
cogvault init [--vault <경로>]
```

1. 설정 파일 — 없으면 생성, 있으면 스킵.
2. `wiki_dir/` — 없으면 생성, 있으면 스킵.
3. `wiki_dir/_schema.md` — 없으면 복사, 있으면 스킵.
4. DB — 없으면 생성 + 전체 인덱싱, 있으면 `CheckConsistency(force=true)`.

멱등. 기존 파일 미덮어쓰기.

### 9.2 search

```
cogvault search [--vault <경로>] [--scope all|wiki|vault] <검색어>
```

### 9.3 serve

```
cogvault serve [--vault <경로>]
```

### 9.4 서버 생명주기

초기화 실패: exit 1 + stderr. 런타임 이상: 도구 에러 반환, 서버 계속.

---

## 10. _schema.md

읽기 전용. 서버가 요약을 instructions로 전달. 강제 타입: `source`만. 나머지 자유.

### 기본 내용

```markdown
# Wiki Schema

## 규칙
- 설정된 wiki 디렉토리 외부 파일은 절대 수정하지 않는다.
- 모든 위키 페이지는 YAML frontmatter를 포함한다.
- 출처 없는 주장은 [TODO: source needed]로 표기한다.
- 소스 원문을 그대로 복사하지 않고 요약·합성한다.
- 모든 사실에는 소스 페이지 [[링크]]를 포함한다.
- LLM이 추측한 내용은 [UNCERTAIN]로 명시한다.

## 사용 가능한 도구
- wiki_read: 파일 읽기 (일부 디렉토리는 접근 제한됨)
- wiki_write: wiki 디렉토리 내 파일 쓰기 (덮어쓰기, _schema.md 제외)
- wiki_list: 디렉토리 브라우징 (비재귀, 경로·제목·타입 포함)
- wiki_search: 전문 검색 (scope: all, wiki, vault)
- wiki_scan: vault 마크다운 파일 경로 목록 (재귀)
- wiki_parse: 노트 메타데이터 파싱 (include_content로 본문 포함 가능)

## 위키 구조 파악
1. wiki_list("<wiki_dir>/")로 최상위 구조 확인 (기본값 예: `"_wiki/"`)
2. wiki_list("<wiki_dir>/entities/") 등으로 하위 탐색
3. wiki_search로 키워드 기반 탐색

## 페이지 타입

### source (필수 스키마)
- 필수 frontmatter: type: source, source_path, ingested_at
- 필수 섹션: ## 요약, ## 핵심 포인트, ## 관련 페이지

### 자유 타입 (선택)
entity, concept, synthesis 등 자유롭게 생성 가능. type 필드 포함 권장.

## Ingest 워크플로우
1. wiki_scan으로 vault 노트 목록 확인
2. wiki_parse(path, include_content=true)로 대상 노트 로드
3. 내용 분석, 핵심 정보 추출
4. wiki_search(query, scope="wiki")로 기존 관련 페이지 확인
5. wiki_write로 sources/ 요약 페이지 생성
6. 필요 시 wiki_read로 관련 페이지 로드 후 wiki_write로 갱신
```

---

## 11. MVP 검증

**성공 기준**: 1주간 매일 사용, 위키가 유용했는가.

- [ ] Day 1: init → 노트 3개 인제스트
- [ ] Day 2~5: 매일 1개 이상 추가
- [ ] Day 3: search로 이전 인제스트 활용
- [ ] Day 7: 위키 우선 참조 습관 형성

| 실패 신호 | 피벗 |
|-----------|------|
| 검색 무용 | 토크나이저 전환 |
| 마찰 높음 | CLI 단축, 워크플로우 단순화 |
| 스키마 미준수 | 스키마 단순화, 도구 description 보강 |
| 품질 낮음 | v0.2 컴파일러 조기 착수 |

---

## 12. 의존성

```
modernc.org/sqlite              # Pure Go SQLite
github.com/mark3labs/mcp-go     # MCP SDK — 버전 고정
github.com/spf13/cobra          # CLI
gopkg.in/yaml.v3                # YAML
github.com/adrg/frontmatter     # frontmatter 파싱
```

---

## 부록 A: 테스트 요건

### Storage
- path traversal → `ErrTraversal`.
- 심볼릭 링크 → `ErrSymlink`.
- `wiki_dir/` 외부 쓰기 → `ErrPermission`.
- `_schema.md` 쓰기 → `ErrPermission`.
- `exclude_read` 읽기 → `ErrPermission`.
- `exclude_read` Exists → `false`.
- 미존재 파일 Read → `ErrNotFound`.
- Write 중간 디렉토리 자동 생성.
- 동시 Write 데이터 손상 없음.

### Index
- Add 후 Search 가능.
- 2글자 이하 한국어 검색어 결과 반환.
- exclude/exclude_read 파일 검색 미포함.
- CheckConsistency(force=true) 후 삭제 파일 미포함.
- scope "wiki"/"vault" 필터.
- bounded staleness: force=false 시 interval 이내 스킵.
- GetMeta 정상 + 미존재 시 ErrNotFound.

### Adapter (Obsidian)
- `[[note]]`, `[[note|alias]]`, `[[note#heading]]` → Links에 `"note"`.
- `![[file]]`, `![[file|size]]` → Attachments에 `"file"`.
- 깨진 frontmatter → 빈 frontmatter + 정상 처리.
- 한글 파일명/경로.
- 바이너리 파일 Scan 건너뜀.
- 비-`.md` Parse → `ErrNotMarkdown`.
- 파일 경로 Scan → `ErrNotFound`.

### MCP 도구
- 정상 round-trip.
- sentinel error 매핑.
- write 후 search 가능.
- list의 title/type = GetMeta 일치.
- parse include_content 동작.
- search scope 필터.
- list/scan에 파일 경로 → ErrNotFound.
- parse에 비-.md → ErrNotMarkdown.

### CLI
- init 멱등성.
- init 시 force=true.
- --vault 미지정 시 현재 디렉토리, 설정 없으면 에러.
- serve 초기화 실패 시 exit 1.

---

## 부록 B: 테스트 fixture

```
testdata/fixtures/
├── basic/                      # 최소 동작
│   ├── .cogvault.yaml
│   ├── _wiki/_schema.md
│   └── notes/hello.md
├── obsidian/                   # 파싱
│   ├── same-name/{a,b}/note.md
│   ├── aliases.md
│   ├── dataview.md
│   ├── embed.md
│   ├── heading-link.md
│   ├── korean-path/한글노트.md
│   └── code-block.md          # v0.2 대비
├── edge/                       # 엣지 케이스
│   ├── broken-frontmatter.md
│   ├── utf8-bom.md
│   ├── empty.md
│   ├── no-frontmatter.md
│   └── binary.png
├── security/                   # 보안
│   ├── traversal/
│   └── private/secret.md
└── real/                       # 실제 vault 서브셋
    └── (구현 전 준비)
```
