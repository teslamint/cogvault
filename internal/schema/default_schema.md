# Wiki Schema

이 문서는 위키 페이지 작성 규칙이자 MCP 서버 지시문이다. 전문은 `wiki_read("_schema.md")`로 읽는다.

## 규칙
- 설정된 wiki 디렉토리 외부 파일은 수정하지 않는다.
- 모든 위키 페이지는 마크다운 + YAML frontmatter로 작성한다.
- source 페이지 필수 frontmatter: `title`, `type: source`, `source_path`, `ingested_at`.
- 출처 없는 주장은 [TODO: source needed]로 표기한다.
- LLM이 추측한 내용은 [UNCERTAIN]로 명시한다.
- 소스 원문을 그대로 복사하지 않고 요약·합성한다.
- `_schema.md`는 쓰기 금지 — 자기 지시서를 보호한다.

## 사용 가능한 도구
- wiki_read: 파일 읽기 (일부 디렉토리는 접근 제한됨)
- wiki_write: wiki 디렉토리 내 파일 쓰기 (덮어쓰기, `_schema.md` 제외)
- wiki_list: 디렉토리 브라우징 (비재귀, 경로·제목·타입 포함)
- wiki_search: 전문 검색 (query, limit)
- wiki_scan: 마크다운 파일 경로 목록 (재귀)
- wiki_parse: 노트 메타데이터 파싱 (include_content로 본문 포함 가능)

## 위키 구조
- `sources/` : 인제스트가 생성한 source 페이지 저장 디렉토리
- 그 외 디렉토리 : 자유 구성 (entity, concept, synthesis 등)
- `_schema.md` : 쓰기 금지

## 페이지 타입

### source (필수 스키마)
- 필수 frontmatter: `title`, `type: source`, `source_path`, `ingested_at`
- 필수 섹션: ## 요약, ## 핵심 포인트, ## 관련 페이지

### 자유 타입 (선택)
entity, concept, synthesis 등 자유롭게 생성 가능. `type` 필드 포함 권장.

## Ingest 워크플로우
인제스트는 자동이다. `cogvault ingest`가 소스 디렉토리를 스캔·요약해 `sources/` 아래 source 페이지를 만든다. 에이전트는 수동 인제스트를 수행하지 않는다. 대신 기존 페이지를 확장·연결한다:
1. wiki_search / wiki_list로 관련 페이지를 찾는다.
2. wiki_read로 내용을 확인한다.
3. wiki_write로 페이지를 보강하거나 [[링크]]로 연결한다.
