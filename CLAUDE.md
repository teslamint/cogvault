# CLAUDE.md — cogvault

이 프로젝트의 스펙은 `SPEC.md`, 설계는 `DESIGN.md`에 있다. 이 문서는 두 문서만으로는 알 수 없는 **맥락, 결정 배경, 기각된 대안, 향후 방향**을 담는다.

## 0. Canonical Context

여러 에이전트(Claude Code, Codex, Gemini 등)가 같은 맥락을 참조할 수 있도록 정본 위치를 분리한다.

- **계약/동작 정본**: `SPEC.md`
- **아키텍처/구현 정본**: `DESIGN.md`
- **결정 기록 정본**: `docs/decisions/`
- **리뷰/조사 기록**: `docs/research/`

원칙:
- 새 기능 계약은 `SPEC.md`에 반영한다.
- 구조/패키지 경계는 `DESIGN.md`에 반영한다.
- 코드/문서에 직접 담기 어려운 채택/보류 판단은 `docs/decisions/`에 기록한다.
- 리뷰 초안, 옵션 비교, 조사 메모는 `docs/research/`에 기록하고, 최종 판단은 `docs/decisions/`로 승격한다.
- `CLAUDE.md`에만 있는 결정은 정본으로 간주하지 않는다.

이 구조의 근거와 유지 원칙은 `docs/decisions/0003-canonical-context-locations.md` 참조.

---

## 1. 프로젝트 기원

Karpathy의 LLM Wiki 패턴(https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f)에서 출발. 핵심 아이디어: RAG 대신 LLM이 마크다운 위키를 점진적으로 빌드하여 지식이 누적되는 구조. 2026-04-04 공개, 2일 만에 2,900+ star.

이 프로젝트는 해당 패턴을 Go 싱글 바이너리 + MCP 서버로 구현하되, **기존 Obsidian vault와 하이브리드 통합**하는 데 초점을 둔다.

---

## 2. 왜 Go인가

대규모 파일 처리를 감안한 언어 선택. 검토한 대안:

| 언어 | 장점 | 기각 이유 |
|------|------|----------|
| **Go (채택)** | 스트리밍 I/O 기본 패턴, goroutine 동시성 (LLM API I/O-bound에 적합), 싱글 바이너리 배포, Pure Go SQLite (CGo 불필요) | — |
| Rust | 메모리 안전, 최고 성능 | 병목이 LLM API 네트워크 호출이지 CPU가 아님. 개발 속도 대비 과잉. |
| TypeScript | MCP SDK 성숙도 최고 | 대규모 파일 처리 시 메모리 관리 불리. |

MCP 프로토콜 자체가 단순(JSON-RPC over stdio)하여 Go의 mcp-go SDK 성숙도가 약간 낮은 건 문제 아님.

---

## 3. 선행 구현 분석 (sage-wiki)

sage-wiki(https://github.com/xoai/sage-wiki)가 가장 직접적인 선행 구현.

**참고한 것**:
- Go + Pure Go SQLite + mcp-go 조합의 실행 가능성 확인
- SQLite FTS5 + 벡터 하이브리드 검색 아키텍처 (우리는 FTS5만 MVP)
- MCP stdio/SSE 이중 전송 패턴
- compile --watch 모드 (우리는 v0.2)

**의도적으로 다르게 한 것**:
- sage-wiki는 API 직접 호출만 지원. 우리는 **LLM 어댑터 패턴** — Claude Code CLI subprocess로 구독 요금제 활용 가능 (v0.2).
- sage-wiki는 vault 위에 오버레이. 우리는 vault 내 설정된 `wiki_dir/` 디렉토리 **하이브리드** — Obsidian 그래프 뷰에서 원본↔위키 연결 시각화.
- sage-wiki는 5-pass 컴파일러를 처음부터 내장. 우리는 **passthrough 우선** — 에이전트가 도구를 직접 조합. 컴파일러는 반복 패턴이 확인된 후 v0.2에서.
- sage-wiki의 온톨로지 그래프(엔티티-관계 BFS)를 분석했으나, MVP에서는 과잉으로 판단하여 제거.

Gist 댓글에서 얻은 교훈:
- bluewater8008: 엔티티 타입별 템플릿 분리, 모든 작업에 wiki 업데이트 강제 → 우리는 MVP에서 source 타입 하나만 강제로 시작.
- peas (Open Claw): "LLM은 편집자, 저자 아님" — 출처 추적 필수 → _schema.md에 `[TODO: source needed]`, `[UNCERTAIN]` 규칙 반영.
- mpazik (Binder): 파일 기반 index.md가 규모 커지면 한계 → `_index.md` 제거, FTS5 + wiki_list로 대체.

---

## 4. 기각된 대안과 이유

### 4.1 아키텍처 수준

| 기각된 안 | 이유 |
|-----------|------|
| 5-pass 컴파일러 (diff→summarize→extract→write→index) | 조기 최적화. 실제 사용 패턴 모르는 상태에서 추상화하면 맞지 않는 구조가 생김. sage-wiki에서 차용 검토했으나 v0.2로 연기. |
| 온톨로지 그래프 (엔티티-관계 BFS, 사이클 감지) | `[[wikilink]]` + FTS5로 대부분 커버. "A와 B가 모순"같은 메타 관계는 Lint에서 LLM이 판단하는 게 더 정확. 복잡도 대비 가치 불분명. |
| `_index.md` 수동 유지 | 에이전트가 페이지 쓰고 인덱스 갱신하는 사이에 크래시하면 불일치. `wiki_list` + FTS5가 항상 정합적인 인덱스 역할. |
| `_log.md` 에이전트 관리 | Ingest 워크플로우에 read→modify→write 패턴이 들어가서 동시성 문제 + 호출 수 증가. MVP에서 제거. v0.2에서 서버 측 자동 로깅 검토. |
| `wiki_delete` 도구 | auto-commit 없는 MVP에서 에이전트 실수로 위키 손실 위험. v0.2로 연기. |
| 페이지 타입 4개 강제 (entity, concept, source, synthesis) | 1주 검증에서 실제로 쓰는 건 source뿐. synthesis는 query file-back인데 MVP 범위 밖. source만 강제, 나머지 자유. |

### 4.2 일관성 모델

| 기각된 안 | 이유 |
|-----------|------|
| 파일 쓰기 + 인덱싱 원자적 트랜잭션 | 파일시스템 + SQLite 크로스 트랜잭션은 롤백 시 파일 복원이 필요. 복잡도 과잉. |
| 인덱싱 배치 (write만 하고 나중에 인덱싱) | 검색이 크게 stale해져서 사용 경험 나쁨. |
| **채택: eventual consistency** | Write-then-index (best-effort) + CheckConsistency(bounded staleness). 단순하고 부분 실패 허용. |

### 4.3 검색

| 기각된 안 | 이유 |
|-----------|------|
| unicode61 토크나이저 | CJK 토큰화 불완전. 한국어 검색 품질 보장 불가. |
| ICU 토크나이저 | Pure Go SQLite(modernc.org)에서 지원 불확실. 외부 의존 증가. |
| 벡터 검색 (sqlite-vec) | API 호출 필요 (임베딩). passthrough 모드에서 API 없이 동작해야 함. v0.3 이후. |
| RRF (Reciprocal Rank Fusion) | 벡터 검색 없이는 의미 없음. v0.3 이후. |
| **채택: trigram** | 3-gram 기반. 한국어 동작. 추가 의존 없음. 2글자 이하 LIKE fallback. |

### 4.4 Adapter / 링크

| 기각된 안 | 이유 |
|-----------|------|
| Scan이 `[]*Source` 반환 | 수천 파일의 Content가 전부 메모리에 적재. 콜백 패턴으로 변경. |
| Links에 대괄호 포함하여 저장 (`[[target]]`) | 소비자(에이전트)가 매번 대괄호를 벗겨야 함. **채택: 대괄호 없는 target만 저장** (예: `"note"`). |
| ResolveLink + BuildCache를 MVP에 포함 | MCP 도구 6개 중 ResolveLink를 호출하는 곳이 없음. Lint(v0.3)에서 필요해질 때 도입. |
| 코드블록 내 wikilink 제외 | 상태 머신 필요. 복잡도 높음. false positive 허용하고 v0.2에서 구현. |
| frontmatter 직접 구현 | 엣지 케이스(빈 frontmatter, `---` 재등장, 깨진 YAML)가 많음. `adrg/frontmatter` 라이브러리 사용. |

### 4.5 LLM 호출

| 기각된 안 | 이유 |
|-----------|------|
| 엔진이 LLM API를 직접 호출 (anthropic API) | API 비용 발생. 구독 요금제 활용 불가. |
| 에이전트가 직접 파일시스템 조작 (MCP 없이) | 경로 보안 없음, FTS5 검색 없음, Obsidian 문법 파싱 없음, 스키마 강제 없음. |
| **채택: passthrough 모드** | 엔진은 도구만 제공, 에이전트가 오케스트레이션. 구독 요금제로 커버. |

---

## 5. 리뷰에서 확립된 원칙

4차례 스펙 리뷰 + 1차 설계 리뷰를 거쳤다. 확립된 원칙:

1. **습관 형성 > 기능 완성**: MVP 성공 기준은 "위키 페이지를 만들 수 있는가"가 아니라 "1주간 매일 쓰고 유용했는가".
2. **passthrough가 기본**: 에이전트 + _schema.md + 저수준 도구만으로 위키 빌드가 가능해야 함. 컴파일러는 편의 기능.
3. **삭제 없는 MVP**: auto-commit 없는 상태에서 에이전트에게 삭제 권한을 주면 위험.
4. **YAGNI 엄격 적용**: 온톨로지, 벡터 검색, 5-pass 컴파일러, ResolveLink 전부 실제 필요성 확인 후.
5. **읽기 보안도 필요**: 쓰기 보안만으로 불충분. `exclude_read`로 민감 디렉토리 보호. `Exists`도 false 반환.
6. **`_schema.md` 쓰기 거부**: 에이전트가 자신의 지시서를 수정하면 의도와 사고 구분 불가.
7. **eventual consistency**: 완벽한 원자성보다 단순한 best-effort + 자동 복구.
8. **bounded staleness**: 매 호출마다 정합성 체크는 과잉. 최소 간격(기본 5초)으로 비용 제어.

---

## 6. v0.2 / v0.3 방향

SPEC/DESIGN에는 MVP만 정의되어 있다. 향후 방향의 맥락:

### v0.2: 컴파일러 자동화

- **LLM 어댑터**: `llm/claudecode.go` — `claude --print` CLI를 subprocess로 호출. **stdin pipe** 필수 (OS ARG_MAX 제한 회피). stderr 캡처로 인증 실패 진단. `--output-format json` 사용 검토. CLI 인터페이스가 Anthropic에 의해 변경될 수 있으므로 `llm/anthropic.go` (API 직접 호출)를 fallback으로 함께 구현.
- **컴파일러**: 2-pass(summarize → index)로 시작. 5-pass는 필요성 확인 후. sage-wiki의 5-pass를 참고하되 그대로 따르지 않음.
- **engine 레이어**: 현재 `mcp/tools.go`에 있는 write-then-index, listWithMeta 등 조합 로직을 engine으로 추출. mcp/와 cmd/ 모두 engine 호출.
- **wiki_delete**: auto-commit과 함께 도입.
- **fsnotify watch 모드**: `compile --watch`.
- **wiki doctor**: CLI 인증 상태, SQLite 접근, vault 구조 사전 검증.
- **wiki_write warnings**: frontmatter 스키마 검증. type 필드 누락 경고 등. 응답의 warnings 배열 활용.
- **코드블록 내 wikilink 제외**: 상태 머신으로 ``` 블록, 인라인 ` 감지.

### v0.3: 고수준 기능

- **query + file-back**: 검색 → LLM 합성 → 결과를 `wiki_dir/synthesis/`에 저장.
- **lint**: 모순, 고아 페이지, 깨진 링크, frontmatter 미준수 검사. 이 시점에 ResolveLink + BuildCache 도입.
- **SSE 전송**: Cloudflare Tunnel 또는 클라우드 배포로 원격 접근.
- **페이지 타입 확장**: 실사용 패턴 보고 entity, concept, synthesis 강제 스키마 추가.

### 이후

- 벡터 검색 (sqlite-vec 또는 외부 임베딩)
- RRF 하이브리드 검색
- 온톨로지 그래프
- git auto-commit
- 다중 vault 지원
- `_index.md` 자동 생성 뷰 (wiki_list 기반 읽기 전용)

---

## 7. 코딩 컨벤션

- Go 표준 프로젝트 레이아웃: `cmd/`, `internal/`
- **인터페이스 기반 설계**: Storage, Index, Adapter 전부 인터페이스. mock으로 단위 테스트.
- **에러 래핑**: `fmt.Errorf("storage.Read %s: %w", path, err)`. 호출 측에서 `errors.Is()`.
- **컨텍스트 전파**: 모든 I/O 및 LLM 호출(v0.2)에 `context.Context`.
- **구조화 로깅**: `log/slog`. 레벨별 필터. 디버깅 시 `slog.Debug`.
- **테스트**: `go test -race ./...`. 인터페이스 mock + testdata/fixtures.
- **의존성**: `go.mod`에 정확한 버전 명시. `go.sum` 커밋. `mcp-go`는 v0.46.0 참조(sage-wiki 기준). 실제 버전은 구현 시점에 확정.
- **config 검증 원칙**: `docs/decisions/0001-config-validation.md` 참조. config/storage 책임 경계, 경로 검증 순서, 에러 형식, KnownFields, AllExcluded 등.
- **storage write 직렬화**: `docs/decisions/0006-storage-write-serialization.md` 참조. MVP는 단일 global mutex.
- **storage 에러 매핑 원칙**: `docs/decisions/0007-storage-error-mapping.md` 참조. sentinel과 raw fs error 경계 정의.
- **adapter 구현 결정**: `docs/decisions/0008-step3-adapter-decisions.md` 참조. 보안 함수 공유, file-level exclude, markdown image 분리, TOCTOU 범위.
- **Step 3 미해결 항목**: `docs/decisions/0009-step3-deferred-items.md` 참조. Step 4 exclude 테스트, Step 5 root 팩토리, pathutil 통합.

---

## 8. 알려진 리스크와 피벗 경로

| 리스크 | 영향 | 피벗 |
|--------|------|------|
| trigram 토크나이저의 한국어 검색 품질 | 2글자 이하 검색어 부정확, 인덱스 크기 3~5배 | unicode61 + trigram 이중 테이블. 또는 ICU 토크나이저 조사. |
| 에이전트가 _schema.md를 잘 안 따름 | 위키 페이지 품질 저하, frontmatter 누락 | 스키마 단순화. MCP 도구 description 보강. instructions 요약 개선. v0.2에서 wiki_write warnings로 피드백. |
| passthrough 모드의 가치 불분명 | "그냥 파일시스템 접근과 뭐가 다른가" | 차별 가치 4가지: 경로 보안, FTS5 검색, Obsidian 파싱, 스키마 강제. 이 4가지가 실사용에서 체감되지 않으면 v0.2 컴파일러를 앞당겨 자체 가치 강화. |
| Claude Code CLI `--print` 인터페이스 변경 (v0.2) | claudecode 어댑터 동작 불가 | anthropic.go API 직접 호출 fallback. CLI 출력 파싱 최소화, JSON 모드 우선. |
| 인제스트 마찰로 사용 중단 | Day 5에 안 쓰게 됨 | CLI 단축 명령 추가. 워크플로우 단순화 (페이지 타입 축소). |
| MCP instructions 크기 제한 | 사용자가 스키마 확장 시 instructions 절삭 | 현재 설계: 2,000자 초과 시 절삭 + wiki_read 안내. 실제로 문제되면 요약 생성 로직 추가. |
| modernc.org/sqlite의 FTS5 trigram 지원 여부 | 빌드 실패 또는 런타임 에러 | 구현 초기(Step 4)에 확인. 미지원 시 unicode61로 fallback 후 한국어 검색 품질 별도 대응. |
| validation 에러에 config 파일 경로 미포함 | 다중 vault/로그 집계 시 원인 파악 어려움 | `docs/decisions/0002-step1-deferred-items.md` 참조. |
| `internal/errors` 패키지명이 stdlib `errors`와 충돌 | 소비자마다 alias import 필요 | `docs/decisions/0002-step1-deferred-items.md` 참조. |
| `Storage.List()`가 child symlink entry를 노출 | list에 보인 경로가 이후 access에서 `ErrSymlink`로 실패할 수 있음 | `docs/decisions/0005-step2-deferred-items.md` 참조. |
| 단일 global write mutex | 병렬 write throughput이 제한될 수 있음 | `docs/decisions/0006-storage-write-serialization.md` 참조. |

---

## 9. 에이전트 사용 시나리오

이 절의 경로 예시는 기본 설정 `wiki_dir: "_wiki"` 기준이다.

### 9.1 Day 1: 최초 Ingest

```bash
# 터미널
cogvault init --vault ~/vaults/my-vault
```

```
# Claude Code (.mcp.json에 wiki 서버 등록 후)
사용자: "notes/project-idea.md를 위키에 인제스트해줘"

에이전트 내부 흐름 (6회 MCP 호출):
1. wiki_scan("notes/")              → 경로 목록 확인
2. wiki_parse("notes/project-idea.md", include_content=true)
                                     → 메타데이터 + 본문
3. (에이전트가 본문 분석, 핵심 추출)
4. wiki_search("project idea", scope="wiki")
                                     → 기존 관련 페이지 확인
5. wiki_write("_wiki/sources/project-idea.md", ...)
                                     → source 페이지 생성
6. wiki_write("_wiki/entities/some-entity.md", ...)
                                     → 관련 엔티티 페이지 생성/갱신
```

### 9.2 Day 3: 검색 활용

```
사용자: "이전에 인제스트한 프로젝트 아이디어에서 기술 스택 관련 내용 찾아줘"

에이전트:
1. wiki_search("기술 스택", scope="wiki")  → 관련 source 페이지
2. wiki_read("_wiki/sources/project-idea.md")  → 상세 확인
3. (에이전트가 응답 생성)
```

### 9.3 Day 7: 습관 형성 판단

- 위키를 먼저 검색하는가, vault 원본을 먼저 보는가?
- wiki_search가 유용한 결과를 반환하는가?
- source 페이지의 품질이 원본보다 접근하기 쉬운가?

---

## 10. 프로젝트명

**확정.** `cogvault`로 확정됨.

확정 기준:
- GitHub에서 가용 (저장소명 미사용)
- Go 모듈 경로로 유효 (`github.com/teslamint/cogvault`)
- npm, PyPI 등에서 충돌 없음 (향후 확장 대비)
- "mesh", "net" 등 네트워크/분산 연상 단어 피하기 (로컬 우선 싱글 바이너리)

**코드 작성 전에 반드시 확정.** 모든 경로(`.cogvault.yaml`, `.cogvault.db`), 설정, CLI 바이너리명이 프로젝트명에 의존.

---

## 11. 구현 시작 체크리스트

- [x] 프로젝트명 확정
- [x] `go mod init github.com/teslamint/cogvault`
- [ ] 의존성 버전 고정 (`go get ... @version`)
- [ ] testdata/fixtures/real/ 준비 (자신의 vault 서브셋)
- [ ] SPEC.md, DESIGN.md, CLAUDE.md를 프로젝트 루트에 배치
- [ ] DESIGN.md Step 1부터 시작
