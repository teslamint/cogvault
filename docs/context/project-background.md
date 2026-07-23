# Project Background

Status: non-canonical background
Last updated: 2026-07-23

This document preserves repository background, historical rationale, and superseded v1 context that should remain searchable after `CLAUDE.md` is slimmed down. Canonical product behavior, architecture, and durable project rules still live in `SPEC.md`, `DESIGN.md`, and accepted decisions under `docs/decisions/` per [0003](../decisions/0003-canonical-context-locations.md) and [0012](../decisions/0012-agent-documentation-governance.md).

## Project Origin

Karpathy의 LLM Wiki 패턴(https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f)에서 출발. 핵심 아이디어: RAG 대신 LLM이 마크다운 위키를 점진적으로 빌드하여 지식이 누적되는 구조. 2026-04-04 공개, 2일 만에 2,900+ star.

이 프로젝트는 해당 패턴을 Go 싱글 바이너리 + MCP 서버로 구현하되, **기존 Obsidian vault와 하이브리드 통합**하는 데 초점을 둔다.

## Why Go

The original language choice optimized for large-file handling and a locally deployable single binary.

| Option | Outcome | Historical rationale |
|---|---|---|
| Go | Chosen | Streaming I/O is a natural fit, goroutine concurrency matches I/O-bound LLM work, single-binary deployment is simple, and Pure Go SQLite avoids CGO. |
| Rust | Rejected early | The expected bottleneck was LLM/network latency rather than CPU throughput, so the extra implementation cost looked unjustified. |
| TypeScript | Rejected early | MCP SDK maturity was attractive, but memory behavior for large local file workflows was less favorable than Go. |

The project also judged that MCP itself was simple enough that a slightly less mature Go SDK was acceptable.

## Prior-Art Analysis

sage-wiki(https://github.com/xoai/sage-wiki)가 가장 직접적인 선행 구현. It was useful mainly as a feasibility and tradeoff baseline, not as a template to copy wholesale.

### What This Repository Borrowed

- Go + Pure Go SQLite + mcp-go 조합의 실행 가능성 확인
- SQLite FTS5 + 벡터 하이브리드 검색 아키텍처 (우리는 FTS5만 MVP)
- MCP stdio/SSE 이중 전송 패턴
- compile --watch 모드 (우리는 v0.2)

### What This Repository Deliberately Changed

- sage-wiki는 API 직접 호출만 지원. 우리는 **LLM 어댑터 패턴** — Claude Code CLI subprocess로 구독 요금제 활용 가능 (v0.2).
- sage-wiki는 vault 위에 오버레이. 우리는 vault 내 설정된 `wiki_dir/` 디렉토리 **하이브리드** — Obsidian 그래프 뷰에서 원본↔위키 연결 시각화.
- sage-wiki는 5-pass 컴파일러를 처음부터 내장. 우리는 **passthrough 우선** — 에이전트가 도구를 직접 조합. 컴파일러는 반복 패턴이 확인된 후 v0.2에서.
- sage-wiki의 온톨로지 그래프(엔티티-관계 BFS)를 분석했으나, MVP에서는 과잉으로 판단하여 제거.

### Additional Lessons from the Early Discussion

- bluewater8008: 엔티티 타입별 템플릿 분리, 모든 작업에 wiki 업데이트 강제 → 우리는 MVP에서 source 타입 하나만 강제로 시작.
- peas (Open Claw): "LLM은 편집자, 저자 아님" — 출처 추적 필수 → _schema.md에 `[TODO: source needed]`, `[UNCERTAIN]` 규칙 반영.
- mpazik (Binder): 파일 기반 index.md가 규모 커지면 한계 → `_index.md` 제거, FTS5 + wiki_list로 대체.

## Rejected Early Alternatives

This section records background rationale. Where an accepted decision now owns the durable outcome, that decision is the canonical rule owner.

### Architecture and Workflow

| Historical alternative | Outcome | Background rationale | Canonical owner when applicable |
|---|---|---|---|
| 5-pass 컴파일러 (diff→summarize→extract→write→index) | Rejected early | 조기 최적화. 실제 사용 패턴 모르는 상태에서 추상화하면 맞지 않는 구조가 생김. sage-wiki에서 차용 검토했으나 v0.2로 연기. | Superseded by [0021](../decisions/0021-v2-refounding.md) and current `DESIGN.md` boundaries |
| 온톨로지 그래프 (엔티티-관계 BFS, 사이클 감지) | Rejected early | `[[wikilink]]` + FTS5로 대부분 커버. "A와 B가 모순"같은 메타 관계는 Lint에서 LLM이 판단하는 게 더 정확. 복잡도 대비 가치 불분명. | Historical only |
| `_index.md` 수동 유지 | Rejected early | 에이전트가 페이지 쓰고 인덱스 갱신하는 사이에 크래시하면 불일치. `wiki_list` + FTS5가 항상 정합적인 인덱스 역할. | Historical only |
| `_log.md` 에이전트 관리 | Rejected early | Ingest 워크플로우에 read→modify→write 패턴이 들어가서 동시성 문제 + 호출 수 증가. MVP에서 제거. v0.2에서 서버 측 자동 로깅 검토. | Historical only |
| `wiki_delete` 도구 | Deferred | auto-commit 없는 MVP에서 에이전트 실수로 위키 손실 위험. v0.2로 연기. | Still reflected in [0021](../decisions/0021-v2-refounding.md) follow-up boundaries |
| 페이지 타입 4개 강제 (entity, concept, source, synthesis) | Rejected early | 1주 검증에서 실제로 쓰는 건 source뿐. synthesis는 query file-back인데 MVP 범위 밖. source만 강제, 나머지 자유. | Historical only |

### Consistency Model

| Historical alternative | Outcome | Background rationale | Canonical owner when applicable |
|---|---|---|---|
| 파일 쓰기 + 인덱싱 원자적 트랜잭션 | Rejected early | 파일시스템 + SQLite 크로스 트랜잭션은 롤백 시 파일 복원이 필요. 복잡도 과잉. | Historical only |
| 인덱싱 배치 (write만 하고 나중에 인덱싱) | Rejected early | 검색이 크게 stale해져서 사용 경험 나쁨. | Historical only |
| **채택: eventual consistency** | Chosen for MVP | Write-then-index (best-effort) + CheckConsistency(bounded staleness). 단순하고 부분 실패 허용. | Current durable implementation boundaries live in `DESIGN.md` and later implementation decisions |

### Search

| Historical alternative | Outcome | Background rationale | Canonical owner when applicable |
|---|---|---|---|
| unicode61 토크나이저 | Rejected early | CJK 토큰화 불완전. 한국어 검색 품질 보장 불가. | Historical only |
| ICU 토크나이저 | Rejected early | Pure Go SQLite(modernc.org)에서 지원 불확실. 외부 의존 증가. | Historical only |
| 벡터 검색 (sqlite-vec) | Deferred | API 호출 필요 (임베딩). passthrough 모드에서 API 없이 동작해야 함. v0.3 이후. | Future follow-up per [0021](../decisions/0021-v2-refounding.md) |
| RRF (Reciprocal Rank Fusion) | Rejected early | 벡터 검색 없이는 의미 없음. v0.3 이후. | Historical only |
| **채택: trigram** | Chosen for MVP | 3-gram 기반. 한국어 동작. 추가 의존 없음. 2글자 이하 LIKE fallback. | Current implementation rationale is preserved in `DESIGN.md` and refounding records |

### Adapter and Link Handling

| Historical alternative | Outcome | Background rationale | Canonical owner when applicable |
|---|---|---|---|
| Scan이 `[]*Source` 반환 | Rejected early | 수천 파일의 Content가 전부 메모리에 적재. 콜백 패턴으로 변경. | Historical only |
| Links에 대괄호 포함하여 저장 (`[[target]]`) | Rejected early | 소비자(에이전트)가 매번 대괄호를 벗겨야 함. **채택: 대괄호 없는 target만 저장** (예: `"note"`). | Historical only |
| ResolveLink + BuildCache를 MVP에 포함 | Deferred | MCP 도구 6개 중 ResolveLink를 호출하는 곳이 없음. Lint(v0.3)에서 필요해질 때 도입. | Historical only |
| 코드블록 내 wikilink 제외 | Deferred | 상태 머신 필요. 복잡도 높음. false positive 허용하고 v0.2에서 구현. | Historical only |
| frontmatter 직접 구현 | Rejected early | 엣지 케이스(빈 frontmatter, `---` 재등장, 깨진 YAML)가 많음. `adrg/frontmatter` 라이브러리 사용. | Historical only |

### LLM Calling Path

| Historical alternative | Outcome | Background rationale | Canonical owner when applicable |
|---|---|---|---|
| 엔진이 LLM API를 직접 호출 (anthropic API) | Rejected early | API 비용 발생. 구독 요금제 활용 불가. | Superseded by [0021](../decisions/0021-v2-refounding.md) |
| 에이전트가 직접 파일시스템 조작 (MCP 없이) | Rejected early | 경로 보안 없음, FTS5 검색 없음, Obsidian 문법 파싱 없음, 스키마 강제 없음. | Historical only |
| **채택: passthrough 모드** | Chosen for MVP | 엔진은 도구만 제공, 에이전트가 오케스트레이션. 구독 요금제로 커버. | Historical only after the v2 refounding, but still useful background |

## Review-Established Principles

4차례 스펙 리뷰 + 1차 설계 리뷰를 거쳤다. 확립된 원칙:

1. **습관 형성 > 기능 완성**: MVP 성공 기준은 "위키 페이지를 만들 수 있는가"가 아니라 "1주간 매일 쓰고 유용했는가".
2. **passthrough가 기본**: 에이전트 + _schema.md + 저수준 도구만으로 위키 빌드가 가능해야 함. 컴파일러는 편의 기능.
3. **삭제 없는 MVP**: auto-commit 없는 상태에서 에이전트에게 삭제 권한을 주면 위험.
4. **YAGNI 엄격 적용**: 온톨로지, 벡터 검색, 5-pass 컴파일러, ResolveLink 전부 실제 필요성 확인 후.
5. **읽기 보안도 필요**: 쓰기 보안만으로 불충분. `exclude_read`로 민감 디렉토리 보호. `Exists`도 false 반환.
6. **`_schema.md` 쓰기 거부**: 에이전트가 자신의 지시서를 수정하면 의도와 사고 구분 불가.
7. **eventual consistency**: 완벽한 원자성보다 단순한 best-effort + 자동 복구.
8. **bounded staleness**: 매 호출마다 정합성 체크는 과잉. 최소 간격(기본 5초)으로 비용 제어.

The durable rule owners for active repository-wide conventions now live in [0022](../decisions/0022-repository-working-conventions.md) and the linked specialized decisions.

## Future Directions and Current Roadmap Context

The canonical v2 product and architecture state lives in [0021](../decisions/0021-v2-refounding.md), `SPEC.md`, `DESIGN.md`, and `docs/specs/2026-07-22-refound-capture-pipeline-design.md`. The list below preserves the broader historical and forward-looking context that used to sit inside `CLAUDE.md`.

### v2 Phase 1 — done (this work)

- **Single mode**: the vault concept is removed; `wiki_dir` is the sole storage root, `sources[]` are external dirs read directly by the ingest pipeline (0021 D1).
- **`cogvault ingest`**: batch pipeline — scan → hash → LLM digest → validate → write → index → ledger → report. Per-file error classes (transient/permanent/infra) and a single-instance flock (0021 D2, D4).
- **`internal/llm`**: adapter interface + `claudecode` backend (`claude --print`, JSON output, stdin, 5m timeout). The old `llm/anthropic.go` API-fallback idea is dropped; the designed escape hatch for CLI changes is now the local-LLM backend (0021 D3).
- **launchd automation**: plist template + README setup for zero-touch scheduled ingest.
- **`scope` removed** from `wiki_search` / `cogvault search` (0021 D5).

### Later Phases Still Under Consideration

- **Local LLM backend** (spike O3): implement the second `llm.Adapter` (ollama / llama.cpp / other) — the primary mitigation for the "Claude CLI changes" risk.
- **Phone capture (S5)**: share-sheet URL → synced inbox folder → same pipeline; consume-and-archive semantics for dedicated inbox dirs (O5). Secondary priority.
- **URL/web-article extraction**: fetch + extract before digest.
- **Periodic digest (S6)**: `cogvault digest` writes a daily/weekly summary page.
- **Markdown-source digestion**: digest raw vault/markdown notes so full-text search over source notes returns (restores the v1 coverage 0021 gave up).
- **Watch mode**: revisit only if launchd schedule latency proves unacceptable (batch + launchd was chosen over a daemon, 0021 D2).

### Carry-Overs That Remained Relevant After Refounding

- **wiki_delete + git auto-commit**: deletion is unsafe without auto-commit; introduce them together.
- **lint**: contradictions, orphan pages, broken links, frontmatter compliance; introduces `ResolveLink` + BuildCache when it lands.
- **SSE transport**: remote access via Cloudflare Tunnel or a cloud deploy.
- **wiki_write warnings**: frontmatter schema-validation feedback (warnings array).
- **code-block wikilink exclusion**: state machine for ``` blocks / inline `.
- **page-type expansion**: enforce entity/concept/synthesis schemas once real usage justifies it.

### Longer-Horizon Ideas

- Vector search (sqlite-vec / external embeddings), RRF hybrid search, ontology graph, multi-wiki support, an auto-generated read-only `_index.md` view.

## Risks and Pivot Paths

The current canonical product state lives elsewhere; this table preserves the broader risk and mitigation context from the agent-facing background.

| Risk | Potential impact | Historical pivot path |
|---|---|---|
| trigram tokenizer Korean search quality | queries ≤2 chars imprecise, index 3-5× larger | unicode61 + trigram dual table, or investigate ICU. (modernc.org/sqlite trigram support confirmed in U4.) |
| Claude Code CLI (`claude --print`) interface or policy change | claudecode backend stops working — now load-bearing | everything behind `llm.Adapter` + JSON output mode; the designed escape hatch is the **local-LLM backend** (0021 D3), not an anthropic-API fallback. |
| Digestion LLM ignores `_schema.md` | low page quality, missing frontmatter | prompt embeds `_schema.md`; an unparsable page is a permanent failure and nothing is indexed; simplify schema / strengthen the prompt. |
| iCloud Drive eviction / dataless files on the wiki dir | consistency re-read forces re-download or fails | stat-gate (size+mtime) avoids needless re-hash; dataless-read errors are per-file warnings, not fatal; DB kept outside the synced folder (absolute `db_path`). |
| launchd execution context differs from a shell | TCC blocks `~/Downloads`, PATH lacks `~/.local/bin/claude`, non-interactive auth fails | plist absolute paths + explicit PATH (O1-verified); README one-time TCC/auth grants. |
| Backlog quota exhaustion during digestion | the PDF backlog burns quota | `--limit` batching; quota failures classified transient (no attempt consumed) so files resume on later runs. |
| MCP instructions size limit | instructions truncated when the user grows the schema | truncate >2,000 chars + point to `wiki_read`; add a summary generator if it bites. |
| `internal/errors` clashes with stdlib `errors` | consumers alias-import | `docs/decisions/0002-step1-deferred-items.md`. |
| `Storage.List()` exposes child symlink entries | a listed path may later fail access with `ErrSymlink` | `docs/decisions/0005-step2-deferred-items.md`. |
| single global write mutex | parallel write throughput limited | `docs/decisions/0006-storage-write-serialization.md`. |

## Historical v1 Context

The sections below are explicitly historical or superseded where the v2 refounding changed repository behavior. They remain useful as archaeology and migration context.

### Historical Agent Usage Scenarios (v1)

이 절의 경로 예시는 기본 설정 `wiki_dir: "_wiki"` 기준이다.

#### 9.1 Day 1: 최초 Ingest

```bash
# 터미널 (v2: --config, 기본 ~/.config/cogvault/config.yaml)
cogvault init --config ~/.config/cogvault/config.yaml
```

```text
# Claude Code (.mcp.json에 wiki 서버 등록 후)
사용자: "notes/project-idea.md를 위키에 인제스트해줘"

에이전트 내부 흐름 (6회 MCP 호출):
1. wiki_scan("notes/")              → 경로 목록 확인
2. wiki_parse("notes/project-idea.md", include_content=true)
                                     → 메타데이터 + 본문
3. (에이전트가 본문 분석, 핵심 추출)
4. wiki_search("project idea")
                                     → 기존 관련 페이지 확인
5. wiki_write("_wiki/sources/project-idea.md", ...)
                                     → source 페이지 생성
6. wiki_write("_wiki/entities/some-entity.md", ...)
                                     → 관련 엔티티 페이지 생성/갱신
```

#### 9.2 Day 3: 검색 활용

```text
사용자: "이전에 인제스트한 프로젝트 아이디어에서 기술 스택 관련 내용 찾아줘"

에이전트:
1. wiki_search("기술 스택")  → 관련 source 페이지
2. wiki_read("_wiki/sources/project-idea.md")  → 상세 확인
3. (에이전트가 응답 생성)
```

#### 9.3 Day 7: 습관 형성 판단

- 위키를 먼저 검색하는가, vault 원본을 먼저 보는가?
- wiki_search가 유용한 결과를 반환하는가?
- source 페이지의 품질이 원본보다 접근하기 쉬운가?

### Historical Project Naming Context (v1)

**확정.** `cogvault`로 확정됨.

확정 기준:
- GitHub에서 가용 (저장소명 미사용)
- Go 모듈 경로로 유효 (`github.com/teslamint/cogvault`)
- npm, PyPI 등에서 충돌 없음 (향후 확장 대비)
- "mesh", "net" 등 네트워크/분산 연상 단어 피하기 (로컬 우선 싱글 바이너리)

**코드 작성 전에 반드시 확정.** 모든 경로(`.cogvault.yaml`, `.cogvault.db`), 설정, CLI 바이너리명이 프로젝트명에 의존.

### Historical Implementation-Start Checklist (v1)

This checklist is completed or superseded. It is retained only as a record of what "ready to start" meant before the v2 refounding:

- [x] 프로젝트명 확정
- [x] `go mod init github.com/teslamint/cogvault`
- [ ] 의존성 버전 고정 (`go get ... @version`)
- [ ] testdata/fixtures/real/ 준비 (자신의 vault 서브셋)
- [ ] SPEC.md, DESIGN.md, CLAUDE.md를 프로젝트 루트에 배치
- [ ] DESIGN.md Step 1부터 시작

## Appendix A: Migration Inventory From the Current `CLAUDE.md`

Every current heading, table row, checklist item, and unique bullet from `CLAUDE.md` is listed below with a destination and one disposition: `moved`, `summarized with canonical link`, or `retained in agent briefing`.

| Source item | Final destination | Disposition |
|---|---|---|
| `## 0. Canonical Context` | `CLAUDE.md` working brief + [0003](../decisions/0003-canonical-context-locations.md) + [0012](../decisions/0012-agent-documentation-governance.md) + [0022](../decisions/0022-repository-working-conventions.md) | summarized with canonical link |
| contract canon bullet (`SPEC.md`) | `CLAUDE.md` working brief + [0003](../decisions/0003-canonical-context-locations.md) | retained in agent briefing |
| architecture canon bullet (`DESIGN.md`) | `CLAUDE.md` working brief + [0003](../decisions/0003-canonical-context-locations.md) | retained in agent briefing |
| decision canon bullet (`docs/decisions/`) | `CLAUDE.md` working brief + [0003](../decisions/0003-canonical-context-locations.md) | retained in agent briefing |
| review/research records bullet (`docs/research/`) | `CLAUDE.md` working brief + [0003](../decisions/0003-canonical-context-locations.md) | retained in agent briefing |
| solved-problems / concepts bullet (`docs/solutions/`, `CONCEPTS.md`) | `CLAUDE.md` working brief | retained in agent briefing |
| approved v2 design bullet | `CLAUDE.md` working brief + [0021](../decisions/0021-v2-refounding.md) + `docs/specs/2026-07-22-refound-capture-pipeline-design.md` | retained in agent briefing |
| v2 refounding decision bullet | `CLAUDE.md` working brief + [0021](../decisions/0021-v2-refounding.md) | retained in agent briefing |
| principle bullet: reflect new feature contracts in `SPEC.md` | `CLAUDE.md` working brief + [0003](../decisions/0003-canonical-context-locations.md) | retained in agent briefing |
| principle bullet: reflect structure and package boundaries in `DESIGN.md` | `CLAUDE.md` working brief + [0003](../decisions/0003-canonical-context-locations.md) | retained in agent briefing |
| principle bullet: record adoption/defer decisions in `docs/decisions/` | `CLAUDE.md` working brief + [0003](../decisions/0003-canonical-context-locations.md) | retained in agent briefing |
| principle bullet: keep review drafts and comparisons in `docs/research/` until promoted | `CLAUDE.md` working brief + [0003](../decisions/0003-canonical-context-locations.md) + [0012](../decisions/0012-agent-documentation-governance.md) | retained in agent briefing |
| principle bullet: decisions only in `CLAUDE.md` are not canonical | `CLAUDE.md` working brief + [0012](../decisions/0012-agent-documentation-governance.md) | retained in agent briefing |
| maintenance bullet: plans are working notes and become stale when canon changes | `CLAUDE.md` working brief + [0012](../decisions/0012-agent-documentation-governance.md) + `docs/plans/2026-07-23-001-docs-agent-documentation-plan.md` | retained in agent briefing |
| maintenance bullet: `AGENTS.md` is a pointer document and must not mirror the body | `CLAUDE.md` working brief + [0012](../decisions/0012-agent-documentation-governance.md) | retained in agent briefing |
| maintenance bullet: after a completed step, update README progress and convention references | [0022](../decisions/0022-repository-working-conventions.md) | summarized with canonical link |
| `## 1. 프로젝트 기원` | `docs/context/project-background.md` | moved |
| `## 2. 왜 Go인가` | `docs/context/project-background.md` | moved |
| language table row: Go chosen for streaming I/O, goroutines, single binary, and Pure Go SQLite | `docs/context/project-background.md` | moved |
| language table row: Rust rejected because the workload is not CPU-bound enough | `docs/context/project-background.md` | moved |
| language table row: TypeScript rejected because of large-file memory concerns | `docs/context/project-background.md` | moved |
| `## 3. 선행 구현 분석 (sage-wiki)` | `docs/context/project-background.md` | moved |
| prior-art bullet: Go + Pure Go SQLite + `mcp-go` feasibility | `docs/context/project-background.md` | moved |
| prior-art bullet: SQLite FTS5 + vector-search hybrid as a reference point | `docs/context/project-background.md` | moved |
| prior-art bullet: MCP stdio/SSE dual transport pattern | `docs/context/project-background.md` | moved |
| prior-art bullet: compile/watch as a later-phase reference | `docs/context/project-background.md` | moved |
| divergence bullet: adapter pattern instead of direct API-only invocation | `docs/context/project-background.md` | moved |
| divergence bullet: hybrid `wiki_dir/` integration inside the vault | `docs/context/project-background.md` | moved |
| divergence bullet: passthrough first, compiler later | `docs/context/project-background.md` | moved |
| divergence bullet: ontology graph judged too heavy for MVP | `docs/context/project-background.md` | moved |
| lesson bullet: only `source` type should be mandatory at first | `docs/context/project-background.md` | moved |
| lesson bullet: LLM is an editor, not an unsourced author | `docs/context/project-background.md` | moved |
| lesson bullet: `_index.md` removal in favor of search/list tools | `docs/context/project-background.md` | moved |
| `## 4. 기각된 대안과 이유` | `docs/context/project-background.md` | moved |
| `### 4.1 아키텍처 수준` | `docs/context/project-background.md` | moved |
| row: five-pass compiler rejected as premature abstraction | `docs/context/project-background.md` | moved |
| row: ontology graph rejected as excessive for MVP | `docs/context/project-background.md` | moved |
| row: manual `_index.md` rejected because of drift on crash | `docs/context/project-background.md` | moved |
| row: `_log.md` rejected because of concurrency/call overhead | `docs/context/project-background.md` | moved |
| row: `wiki_delete` deferred because deletion was unsafe | `docs/context/project-background.md` | moved |
| row: four mandatory page types rejected because usage justified only `source` | `docs/context/project-background.md` | moved |
| `### 4.2 일관성 모델` | `docs/context/project-background.md` | moved |
| row: cross-filesystem and SQLite atomic transaction rejected | `docs/context/project-background.md` | moved |
| row: delayed indexing rejected because search would be stale | `docs/context/project-background.md` | moved |
| row: write-then-index eventual consistency chosen | `docs/context/project-background.md` | moved |
| `### 4.3 검색` | `docs/context/project-background.md` | moved |
| row: `unicode61` tokenizer rejected | `docs/context/project-background.md` | moved |
| row: ICU tokenizer rejected | `docs/context/project-background.md` | moved |
| row: vector search deferred | `docs/context/project-background.md` | moved |
| row: RRF rejected without vector retrieval | `docs/context/project-background.md` | moved |
| row: trigram chosen with short-query fallback | `docs/context/project-background.md` | moved |
| `### 4.4 Adapter / 링크` | `docs/context/project-background.md` | moved |
| row: `Scan` should not return fully loaded sources | `docs/context/project-background.md` | moved |
| row: link targets should be stored without brackets | `docs/context/project-background.md` | moved |
| row: ResolveLink/BuildCache deferred from MVP | `docs/context/project-background.md` | moved |
| row: code-block wikilink exclusion deferred | `docs/context/project-background.md` | moved |
| row: frontmatter should use a library, not custom parsing | `docs/context/project-background.md` | moved |
| `### 4.5 LLM 호출` | `docs/context/project-background.md` | moved |
| row: direct Anthropic API calls rejected | `docs/context/project-background.md` | moved |
| row: agent-only filesystem manipulation rejected | `docs/context/project-background.md` | moved |
| row: passthrough mode chosen as the base approach | `docs/context/project-background.md` | moved |
| `## 5. 리뷰에서 확립된 원칙` | `docs/context/project-background.md` + [0022](../decisions/0022-repository-working-conventions.md) | summarized with canonical link |
| principle item 1: habit formation over feature completeness | `docs/context/project-background.md` | moved |
| principle item 2: passthrough as the default starting point | `docs/context/project-background.md` | moved |
| principle item 3: no deletion in MVP | `docs/context/project-background.md` | moved |
| principle item 4: strict YAGNI | `docs/context/project-background.md` | moved |
| principle item 5: read security matters too | `docs/context/project-background.md` | moved |
| principle item 6: `_schema.md` writes must be blocked | `docs/context/project-background.md` | moved |
| principle item 7: eventual consistency beats premature atomicity | `docs/context/project-background.md` | moved |
| principle item 8: bounded staleness over per-call full checks | `docs/context/project-background.md` | moved |
| `## 6. Roadmap (v2)` | `docs/context/project-background.md` + [0021](../decisions/0021-v2-refounding.md) + [0014](../decisions/0014-roadmap-adoption-boundaries.md) | summarized with canonical link |
| heading: v2 Phase 1 done | `docs/context/project-background.md` | moved |
| roadmap bullet: single mode around `wiki_dir` and `sources[]` | `docs/context/project-background.md` | moved |
| roadmap bullet: `cogvault ingest` pipeline | `docs/context/project-background.md` | moved |
| roadmap bullet: `internal/llm` and `claudecode` backend | `docs/context/project-background.md` | moved |
| roadmap bullet: launchd automation | `docs/context/project-background.md` | moved |
| roadmap bullet: `scope` removed from search surfaces | `docs/context/project-background.md` | moved |
| heading: Later phases | `docs/context/project-background.md` | moved |
| roadmap bullet: local LLM backend | `docs/context/project-background.md` | moved |
| roadmap bullet: phone capture | `docs/context/project-background.md` | moved |
| roadmap bullet: URL/web extraction | `docs/context/project-background.md` | moved |
| roadmap bullet: periodic digest | `docs/context/project-background.md` | moved |
| roadmap bullet: Markdown-source digestion | `docs/context/project-background.md` | moved |
| roadmap bullet: watch mode only if schedule latency is inadequate | `docs/context/project-background.md` | moved |
| heading: Still-relevant carry-overs | `docs/context/project-background.md` | moved |
| carry-over bullet: `wiki_delete` with auto-commit | `docs/context/project-background.md` | moved |
| carry-over bullet: lint for contradictions/orphans/broken links | `docs/context/project-background.md` | moved |
| carry-over bullet: SSE transport | `docs/context/project-background.md` | moved |
| carry-over bullet: `wiki_write` warnings | `docs/context/project-background.md` | moved |
| carry-over bullet: code-block wikilink exclusion | `docs/context/project-background.md` | moved |
| carry-over bullet: page-type expansion | `docs/context/project-background.md` | moved |
| heading: Beyond | `docs/context/project-background.md` | moved |
| beyond bullet: vector or hybrid retrieval | `docs/context/project-background.md` | moved |
| beyond bullet: ontology graph | `docs/context/project-background.md` | moved |
| beyond bullet: multi-wiki support | `docs/context/project-background.md` | moved |
| beyond bullet: generated read-only `_index.md` | `docs/context/project-background.md` | moved |
| `## 7. 코딩 컨벤션` | [0022](../decisions/0022-repository-working-conventions.md) | summarized with canonical link |
| convention bullet: standard Go layout (`cmd/`, `internal/`) | [0022](../decisions/0022-repository-working-conventions.md) | moved |
| convention bullet: interface-driven design for Storage/Index/Adapter and mockable tests | [0022](../decisions/0022-repository-working-conventions.md) | moved |
| convention bullet: error wrapping with `%w` and `errors.Is()` at call sites | [0022](../decisions/0022-repository-working-conventions.md) | moved |
| convention bullet: `context.Context` for I/O and LLM calls | [0022](../decisions/0022-repository-working-conventions.md) | moved |
| convention bullet: structured logging with `log/slog` and `slog.Debug` | [0022](../decisions/0022-repository-working-conventions.md) | moved |
| convention bullet: verify with `go test -race ./...`, mocks, and fixtures | [0022](../decisions/0022-repository-working-conventions.md) | moved |
| convention bullet: exact dependency versions, commit `go.sum`, `mcp-go` version fixed at implementation time | [0022](../decisions/0022-repository-working-conventions.md) | moved |
| convention bullet: config validation boundary | [0022](../decisions/0022-repository-working-conventions.md) + [0001](../decisions/0001-config-validation.md) | summarized with canonical link |
| convention bullet: storage write serialization | [0022](../decisions/0022-repository-working-conventions.md) + [0006](../decisions/0006-storage-write-serialization.md) | summarized with canonical link |
| convention bullet: storage error mapping | [0022](../decisions/0022-repository-working-conventions.md) + [0007](../decisions/0007-storage-error-mapping.md) | summarized with canonical link |
| convention bullet: adapter implementation decisions | [0022](../decisions/0022-repository-working-conventions.md) + [0008](../decisions/0008-step3-adapter-decisions.md) | summarized with canonical link |
| convention bullet: Step 3 deferred items | [0022](../decisions/0022-repository-working-conventions.md) + [0009](../decisions/0009-step3-deferred-items.md) | summarized with canonical link |
| convention bullet: CLI implementation decisions | [0022](../decisions/0022-repository-working-conventions.md) + [0015](../decisions/0015-step6-cmd-decisions.md) | summarized with canonical link |
| convention bullet: Step 6 deferred items | [0022](../decisions/0022-repository-working-conventions.md) + [0016](../decisions/0016-step6-deferred-items.md) | summarized with canonical link |
| `## 8. Known risks and pivot paths` | `docs/context/project-background.md` | moved |
| risk row: trigram tokenizer Korean quality | `docs/context/project-background.md` | moved |
| risk row: Claude Code CLI change risk | `docs/context/project-background.md` | moved |
| risk row: `_schema.md` ignored by the digestion model | `docs/context/project-background.md` | moved |
| risk row: iCloud Drive dataless files | `docs/context/project-background.md` | moved |
| risk row: launchd context differs from a shell | `docs/context/project-background.md` | moved |
| risk row: backlog quota exhaustion | `docs/context/project-background.md` | moved |
| risk row: MCP instruction-size limit | `docs/context/project-background.md` | moved |
| risk row: `internal/errors` name clash | `docs/context/project-background.md` | moved |
| risk row: `Storage.List()` exposes child symlink entries | `docs/context/project-background.md` | moved |
| risk row: single global write mutex throughput limit | `docs/context/project-background.md` | moved |
| `## 9. 에이전트 사용 시나리오` | `docs/context/project-background.md` | moved |
| scenario heading: Day 1 first ingest | `docs/context/project-background.md` | moved |
| scenario heading: Day 3 search usage | `docs/context/project-background.md` | moved |
| scenario heading: Day 7 habit-formation check | `docs/context/project-background.md` | moved |
| Day 7 bullet: does the user search the wiki first | `docs/context/project-background.md` | moved |
| Day 7 bullet: does `wiki_search` return useful results | `docs/context/project-background.md` | moved |
| Day 7 bullet: are source pages easier to use than originals | `docs/context/project-background.md` | moved |
| `## 10. 프로젝트명` | `docs/context/project-background.md` | moved |
| naming bullet: GitHub repository name available | `docs/context/project-background.md` | moved |
| naming bullet: Go module path valid | `docs/context/project-background.md` | moved |
| naming bullet: package registry collision risk low | `docs/context/project-background.md` | moved |
| naming bullet: avoid network/distributed wording | `docs/context/project-background.md` | moved |
| naming warning: finalize before writing code because names affect paths/config/binary names | `docs/context/project-background.md` | moved |
| `## 11. 구현 시작 체크리스트` | `docs/context/project-background.md` | moved |
| checklist item: project name fixed | `docs/context/project-background.md` | moved |
| checklist item: `go mod init github.com/teslamint/cogvault` | `docs/context/project-background.md` | moved |
| checklist item: pin dependency versions | `docs/context/project-background.md` | moved |
| checklist item: prepare real `testdata/fixtures` | `docs/context/project-background.md` | moved |
| checklist item: place `SPEC.md`, `DESIGN.md`, and `CLAUDE.md` at root | `docs/context/project-background.md` | moved |
| checklist item: start from Design Step 1 | `docs/context/project-background.md` | moved |
