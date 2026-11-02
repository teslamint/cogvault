# 근거 확인 상태: MCP 응답과 lint 구현 인계안

> 상태: 구현됨(2026-11-02, 미커밋). 정본은 `SPEC.md`, `DESIGN.md`, `docs/decisions/0026-research-evidence-record.md`이며 이 문서는 비정본 작업 계획으로 남긴다. 계획에서 열려 있던 세부(claim `id`는 문자열, `severity`는 EVID001–003 `error`·EVID004/005/007 `warning`, 잘못된 check/gap `kind` 항목의 EVID005·EVID007 생략, `wiki_write` 경고는 기존 경고 뒤에 진단 정렬 순서로 추가)는 정본에서 확정했다.

## 목표와 경계

에이전트가 자료를 합성할 때 **실제로 수행한 확인**, **남은 공백**, **다음 확인 방법**을 기록하고, 읽기·검색에서 한계를 볼 수 있게 한다. 논문·정책 자료·기술 문서·역사 기록·법령 등 자료 종류에 관계없이 출처가 실제로 뒷받침하는 범위와 작성하려는 주장의 범위를 구분한다. 범위를 넓혀 해석할 때 필요한 가정과 한계를 구체적으로 기록한다. `lint`와 `wiki_write`는 동일한 구조 검증을 사용한다. 통과 표시를 사실 확인 완료 또는 법적 판단으로 부르지 않는다. 검증 상태는 작성자의 자기 보고이며 외부 문서의 주장만으로 자동 승격하지 않는다.

초기 적용 대상은 `research_evidence`를 선언한 위키 Markdown 페이지다. 기존 페이지에는 새 필드가 없다는 이유로 문제를 보고하지 않는다. 대량 이관, 위키 `_schema.md` 갱신·마이그레이션, 원문 자동 수집, 재현 분석 실행, 정보공개청구, 인과·규범 판단의 자동 판정은 범위 밖이다.

## 기존 계약과 변경 지점

- 현재 `internal/mcp/server.go`의 `NewServer`는 `server.WithInstructions(schemaInstructions(cfg, store))`를 넘긴다. `schemaInstructions`는 위키 `_schema.md`(또는 번들 기본본)를 최대 2,000 rune까지 싣는다. v1에서는 **서버 소유의 짧은 근거 지침을 별도 상수/함수로 만들고**, 이 기존 함수의 반환값과 결합한 문자열을 `WithInstructions`에 넘긴다. 기존 스키마 로딩·절단·전문 읽기 안내·쓰기 보호는 변경하지 않는다. 2,000 rune은 현재 스키마 부분의 코드 제한이며 MCP 전체 instructions의 외부 제한으로 주장하지 않는다. 새 서버를 배포·재시작하고 클라이언트를 필요 시 재연결·재초기화해야 새 지침이 보인다. 사용자의 위키 파일 갱신은 필요하지 않다.
- 서버 소유 지침에는 수행한 확인·남은 공백·다음 경로의 구분, 출처 범위와 주장 범위의 차이·확장 가정 기록, 미확인 상태의 자동 승격 금지, lint 통과가 사실 검증을 뜻하지 않는다는 점을 담는다. 상세 `research_evidence` 필드 계약은 프로젝트 `SPEC.md`에 두고, MCP에는 짧은 작성 원칙과 검색·쓰기 응답을 따르라는 안내를 제공한다. 이 지침 자체는 별도 상수와 테스트로 버전 관리하며 위키 스키마 내용과 혼동하지 않는다.
- `internal/mcp/server.go`의 `wiki_read`·`wiki_search` 도구 설명에도 핵심 사용 지침을 간결히 반복할 수 있다. `wiki_read`의 응답 본문은 바꾸지 않는다.
- `internal/mcp/tools.go`의 `wiki_read`는 원문 텍스트 반환 계약을 유지한다. `wiki_search`는 결과마다 파일을 읽어 `usage_terms`를 요약한다. 동일한 접근 제한과 결과별 실패 격리를 지키면서 `research_evidence` 요약을 **추가 필드**로 넣을 수 있다. 읽지 못하거나 파싱할 수 없으면 `unavailable`과 이유를 반환하고 다른 검색 결과는 유지한다. 검색 요약은 본문을 대체하지 않는다.
- `wiki_write`는 저장 후 `warnings: string[]`를 돌려준다. 새 진단은 기존 경고를 지우거나 쓰기를 차단하지 않고 문자열로 추가한다. 구조화 진단 필드를 추가할 경우 별도 필드로 두고 기존 키·타입·`status: written`을 보존한다.
- `cmd/cogvault/lint.go`는 현재 텍스트 출력만 지원하며 `parse`, `frontmatter`, `broken-link`, `orphan` 문제를 보고한다. 기본 종료 0, `--strict`는 어떤 문제든 비영이며 출력은 동일하다. 새 근거 기록 문제도 이 계약을 따른다. v1에서 `status --json`과 같은 불리언 `lint --json`을 추가한다. `_schema.md`의 기존 예외와 링크 검사도 유지한다.

## 제안 기록 형식

`research_evidence`는 선택적이다. 블록이 있으면 검증하고, `version`이 없거나 1이 아니면 EVID001을 낸다. 이름은 초안이며 더 포괄적인 이름으로 바꿔도 된다. v1 필드와 값은 구현 전 프로젝트 `SPEC.md`에 고정한다. 이는 위키 `_schema.md` 지시문 갱신 요구와 별개다. 확인 단계와 공백은 한 상태값에 섞지 않는다. 원문 대조 완료, 수치 재계산 대기, 적용 범위 미확인을 동시에 나타낼 수 있어야 한다. 확인 종류는 자료 유형에 따라 선택한다. 모든 주장에 표 대조나 재현을 요구하지 않는다.

v1의 닫힌 값 집합은 `claims[].kind = empirical | causal | normative`, `checks[].kind = bibliography | fulltext | table | replication`, `gaps[].kind = source_unavailable | verification_pending | replication_pending | applicability_unknown | additional_evidence_needed`로 한다. `verification_pending`은 아직 수행하지 않은 출처·수치 확인, `replication_pending`은 계산·실행 재현 대기, `applicability_unknown`은 출처와 주장 범위의 연결 미확인, `additional_evidence_needed`는 별도 자료 필요를 뜻한다. `source_unavailable`은 필요한 원문·자료를 확보하지 못했음을 뜻한다. 새 값은 v1에서 조용히 허용하지 말고 버전과 정본 계약을 갱신한다. 본문이나 `detail`의 자유 텍스트를 enum처럼 판정하지 않는다.

```yaml
---
title: 자료 근거 검토
type: synthesis
sources:
  - sources/자료-A.md
research_evidence:
  version: 1
  claims:
    - id: claim-1
      text: 자료 A는 항목별 집계값을 제시한다.
      kind: empirical
      sources:
        - ref: sources/자료-A.md
          locator: 표 2
      checks:
        - kind: bibliography
          result: 작성 주체·제목·발행 시점 대조
          locator: 표지
        - kind: table
          result: 표 2의 항목별 수치 대조
          locator: 표 2
      gaps:
        - kind: replication_pending
          detail: 재계산에 필요한 입력은 찾았으나 실행하지 않음
          next_step: 입력 버전과 계산 절차를 고정해 재현
        - kind: applicability_unknown
          detail: 자료 A는 원래 집계 범위를 다루며, 현재 주장에 필요한 다른 범위는 다루지 않음
          next_step: 두 범위의 차이와 확장에 필요한 가정을 검토
      limitation: 원래 집계 범위 밖에 적용하려면 추가 가정이 필요함
---
```

`sources`는 기존 synthesis 출처 목록과 함께 둘 수 있지만, v1 claim의 `sources[].ref`가 페이지 수준 `sources`의 부분집합일 필요는 없다. `ref`는 `sources/자료-A.md`처럼 **위키 루트 기준의 정확한 상대 Markdown 파일 경로**다. 폴더와 `.md` 확장자를 포함하고, 실제 그 경로의 파일을 확인한다. 절대경로·위키 루트 이탈·URL·`[[위키링크]]`·제목/별칭·`#` 단편·확장자 생략은 허용하지 않는다. basename 검색, `sources/` 자동 접두, 첫 일치 파일 선택도 하지 않는다. 대소문자와 유니코드 정규화 차이는 별도로 판정하지 않고 위키 파일 시스템의 경로 조회 결과를 따른다. 따라서 대소문자를 구분하지 않는 파일 시스템에서는 대소문자만 다른 ref도 해결될 수 있다. 경로의 안전성 검사는 기존 위키 루트 저장소 규칙을 재사용하며, ref만을 위해 외부 네트워크에 접근하지 않는다. 파일이 이동·개명되면 ref도 갱신해야 한다. 이 규칙은 근거 필드에만 적용하고 본문 위키링크 해석은 바꾸지 않는다. URL 자료는 위키 내부 출처 페이지로 연결하거나 후속 버전에 외부 참조 형식을 정의한다. 출처 안의 위치는 ref의 단편이 아닌 별도 `locator`에 적는다. 출처 원문 접근 실패는 `fulltext` 완료의 근거가 아니다. v1은 `research_evidence` 내부의 알 수 없는 추가 키를 검증할 때 무시한다. 필드명 유사성으로 오타를 추정하지 않는다. 필수 필드의 오타는 원래 필드의 누락으로 보고 해당 진단(예: `result` 오타는 EVID005)을 낸다. 선택 필드의 오타(예: `limitaton`)는 진단하지 못하며 그 값은 표준 필드로 해석하지 않는다. 추가 키를 검증에서 무시해도 저장 시 삭제하거나 원본 문서를 다시 쓰지 않는다. 인식된 `kind` 필드의 값이 닫힌 enum 밖이면 추가 키와 달리 EVID003이다.

각 `checks` 항목은 완료한 확인의 자기 보고이며 별도 status를 두지 않는다. 모든 항목에 비어 있지 않은 `result`가 필요하다. `fulltext`·`table`·`replication`에는 비어 있지 않은 `locator`도 필요하다. `replication`의 `locator`는 재현 산출물의 위키 경로 또는 문서 안의 특정 위치를 가리킨다. 단순 입력·절차 발견이나 실패한 시도는 완료 확인으로 기록하지 않는다. 단계별 날짜·도구·실행 환경은 선택적 추적 필드다. 각 gap에는 비어 있지 않은 `detail`과 `next_step`이 필요하다. 검증 결과나 공백이 없는 주장은 허용하되 ‘확인됨’으로 요약하지 않는다. 숫자가 없는 문서에는 `table` 검사를 강제하지 않는다.

잘못된 기록의 예: `checks: [{kind: replication}]`처럼 결과·산출물 없이 완료를 선언하는 경우. `replication` 완료와 `replication_pending`이 같은 claim에 있어도 출처·버전·확인 대상이 다를 수 있으므로 v1 lint는 모순으로 판정하지 않는다. `fulltext` 완료와 `replication_pending`도 유효하다. 필요한 경우 작성자가 gap의 대상과 시점을 `detail`에 구분해 기록한다.

## 공통 검증기와 진단

`internal/evidence` 같은 순수 패키지에 파서·검증기·요약기를 둔다. 입력은 위키 경로, 원시 페이지 바이트, 위키 경로 확인에 필요한 제한된 읽기 인터페이스다. 현재 CLI lint의 adapter Parse, `wiki_write`의 frontmatter 처리, `internal/terms`의 Parse가 다르므로 근거 필드는 이 공통 파서 한 곳에서 해석한다. 파일을 쓸 수 없고, 네트워크·LLM을 호출하지 않는다. `wiki_write`와 CLI lint가 같은 검증 결과를 받아 같은 진단 ID를 쓰도록 한다.

근거 진단의 내부 구조에는 `id`, `path`, `claim_id`(해당 시), `severity`, `message`, `fix_hint`를 둔다. 기존 비근거 lint 문제에는 새 ID를 강제하지 않는다. 모든 lint 문제는 경로→종류→claim ID→진단 ID→메시지 순으로 정렬한다(해당 없는 값은 빈 문자열). 기존 map 순회로 인한 출력 순서 변경이므로 `SPEC.md` §9.12에 적는다. v1 진단 ID:

| ID | 검사 |
| --- | --- |
| `EVID001` | `research_evidence`가 선언됐으나 version이 없거나 정수 1이 아님. 해당 페이지의 하위 근거 진단은 생략 |
| `EVID002` | `claims`가 배열이 아니거나 항목이 객체가 아님, claim의 비어 있지 않은 ID·text·kind가 없거나 ID가 중복됨. kind의 미지원 값은 EVID003 |
| `EVID003` | `checks`·`gaps`가 있을 때 배열이 아님, 그 배열의 항목이 객체가 아님, check/gap의 `kind`가 누락·빈 문자열·문자열 이외 유형·닫힌 값 집합 밖임, 또는 claim의 인식된 `kind`가 닫힌 값 집합 밖임 |
| `EVID004` | claim의 sources가 비었거나 배열이 아님, 항목이 객체가 아님, ref가 비어 있거나 문자열이 아님, 정확한 상대 `.md` 경로 형식·안전성 검사를 통과하지 못함, 또는 해당 경로의 파일이 없음 |
| `EVID005` | check의 result가 비어 있거나 fulltext/table/replication의 locator가 비어 있음 |
| `EVID006` | 예약: v1에서는 발생시키지 않음. 동일 확인 작업의 출처·버전·대상 식별 설계 후 별도 버전에서 검토 |
| `EVID007` | gap의 detail 또는 next_step이 비어 있음 |

지원하지 않는 버전에서는 추측해 하위 필드를 해석하지 않고 EVID001 하나를 우선한다. `claims` 구조가 잘못됐으면 EVID002, `checks`·`gaps` 구조나 항목의 `kind`가 잘못됐으면 EVID003을 낸다. 해당 배열·항목을 안전하게 읽을 수 없으면 그 하위의 EVID004·EVID005·EVID007은 생략한다. claim의 `kind` 누락은 EVID002이고, 존재하지만 미지원 값은 EVID003이다. `checks`·`gaps` 자체는 선택 필드이며 없으면 진단하지 않는다. 심각도는 내부·JSON 출력에 기록하되 첫 도입에서 `--strict`의 기존 ‘모든 문제에 실패’ 계약을 바꾸지 않는다. `wiki_write`의 기존 `warnings: string[]`에는 `[EVID005] claim-1: ...; fix: ...`처럼 식별자와 해결 힌트를 담는다. **CLI 기본 텍스트에는 EVID ID를 표시하지 않고** 기존 `<kind> <path> <message>` 모양을 유지하며 새 종류는 `evidence`로 둔다. 텍스트 message에는 claim ID와 해결 힌트를 사람이 읽을 수 있게 넣되 EVID 코드를 중복 삽입하지 않는다. 외부 출처의 내용·프롬프트는 검증기 명령으로 취급하지 않는다.

`cogvault lint --json`은 제안하는 새 출력 모드다. stdout에는 단일 객체 `{"issues":[...]}`만 출력한다. 각 issue는 `kind`, `path`, `message`를 필수로 가지며 근거 문제에는 `id`, `claim_id`, `severity`, `fix_hint`를 추가한다. 기존 비근거 문제의 `id`는 빈 문자열로 두며 새 코드를 발명하지 않는다. `issues`는 위의 텍스트 정렬과 동일한 순서의 배열이고 문제가 없으면 `{"issues":[]}`이다. JSON에서는 텍스트 요약 줄을 출력하지 않는다. `--strict`는 두 출력 모드 모두 동일한 issues에 대해 종료 코드만 바꾸며, 같은 출력 모드의 stdout은 `--strict` 유무에 관계없이 같다. 기존 기본 텍스트와 `warnings: string[]`의 타입·동작은 유지한다.

lint가 검사할 수 없는 것: 출처가 실제로 주장을 지지하는지, 추론 방식이 타당한지, 규범 판단이 옳은지, 출처의 범위를 다른 대상·시점·조건·결과에 확장할 수 있는지. 장소·시대·자료 유형 같은 표면적 속성만으로 적용 한계를 자동 부여하지 않는다. 검색 응답에서도 `checks`를 독립 검증 인증처럼 표현하지 않고, 출처 범위와 주장 범위 사이에 기록된 차이와 가정을 짧게 제시한다.

## 검색 요약의 v1 계약

기존 결과 필드와 `usage_terms`는 유지하고 결과마다 `research_evidence` 객체를 추가한다. 형식은 `{status, claim_count, claims, truncated, reason}`이며 `status`는 `absent | recorded | unavailable`, `reason`은 `unavailable`일 때 짧은 설명이고 그 밖에는 빈 문자열이다. `recorded`는 작성된 기록이 있다는 뜻이지 검증 인증이 아니다. `claims`는 원래 문서 순서대로 최대 3개를 포함하며 각 항목은 `{id, kind, checks, gaps, limitation}`이다. `checks`와 `gaps`는 각 kind 문자열만 원래 순서대로 최대 4개를 담는다. `limitation`은 작성자가 기록한 문장을 최대 200 rune까지 싣는다. 더 많은 claim·항목 또는 긴 limitation이 생략되면 `truncated: true`로 표시하고 `claim_count`는 전체 claim 수를 보존한다. `absent`와 `unavailable`은 빈 claims, count 0, truncated false다. 다음은 `unavailable`이며 다른 결과는 계속 반환한다: 페이지 읽기 실패, frontmatter·근거 블록의 YAML 파싱 실패, EVID001(`version` 누락 또는 1 이외 값, 이유에 지원하지 않는 버전임을 적음), 그리고 v1 기록에 EVID002 또는 EVID003이 하나라도 있는 경우(기록 전체를 요약하지 않으며 이유에 구조 오류임을 적음). 일부 claim만 골라 요약하지 않는다. EVID004·EVID005·EVID007만 있는 v1 기록은 구조를 읽을 수 있으므로 `recorded`로 요약한다. 검색은 이미 결과별로 읽는 페이지 바이트만 파싱하고 출처 ref를 해결하지 않는다. EVID004의 경로 확인은 쓰기와 CLI lint에서 수행한다. 검색 요청당 위키 전체 스캔, 외부 URL 접근, LLM 호출을 추가하지 않는다.

## 구현 순서

1. `SPEC.md`에 선택적 형식, MCP 결과의 추가 필드, 진단과 lint 계약을 적고 `DESIGN.md`에 검증기 경계를 적는다. 지속적 규칙이라면 결정 기록도 작성한다.
2. `NewServer`에서 기존 `schemaInstructions` 결과에 서버 소유 근거 지침을 별도로 결합한다. 스키마 부분의 2,000 rune 처리와 `_schema.md` 보호는 유지한다. 새 서버 초기화 때 지침이 보이는지 시험한다. 이 단계는 위키 스키마 파일을 수정하지 않는다.
3. `internal/evidence`의 구조 파싱·검증·요약을 구현한다. ref 안전성 검사는 기존 위키 루트 저장소 규칙을 재사용하되, 파일 존재 확인에는 축약형 위키링크 resolver를 쓰지 않는다.
4. `wiki_write` 경고와 `wiki_search` 결과에 연결한다. `wiki_read`의 원문 반환은 유지한다. 접근 실패 시 검색 결과 전체를 실패시키지 않는다.
5. CLI lint에 같은 진단을 연결한다. 기존 검사를 제거하지 않고 모든 문제의 결과 순서를 안정화한다.

## 수용 테스트

- 유효한 부분 확인 예시가 저장·검색·lint를 통과하고, 요약에 `table`과 `replication_pending`, 기록된 한계가 함께 남는다. 숫자 없는 자료의 확인 기록에는 표·재현 단계가 없어도 된다.
- 빠진 ID, 중복 ID, 존재하지 않는 정확한 ref 경로, 절대경로·루트 이탈·URL·위키링크·단편·확장자 생략 ref, 미지원 버전, 닫힌 enum 밖 값, 결과·위치 없는 완료 확인은 각각 지정된 ID로 진단된다. basename·접두어 대체 경로만 존재해도 ref는 실패한다. 대소문자만 다른 ref의 결과는 파일 시스템에 따르므로 수용 테스트로 고정하지 않는다. 본문 위키링크는 기존 규칙을 따른다. EVID006은 v1에서 발생하지 않는다. `replication` 완료와 `replication_pending`이 함께 있어도 자동 모순 판정하지 않는다.
- 알 수 없는 추가 키와 선택 필드 오타는 v1에서 진단하지 않고 원문 저장·읽기 왕복에서 보존한다. 필수 필드 오타는 해당 필드 누락 진단을 내며, 인식된 `kind`의 알 수 없는 값은 EVID003을 낸다.
- `checks`·`gaps`가 배열이 아니거나 항목이 객체가 아니거나 check/gap `kind`가 누락·빈 문자열·문자열 이외 유형이면 EVID003을 낸다. 잘못된 항목의 하위 필드 누락을 중복 진단하지 않으며, `checks`·`gaps` 필드가 없으면 통과한다. claim `kind` 누락은 EVID002, 미지원 값은 EVID003이다.
- 같은 잘못된 페이지에 대해 MCP 쓰기 경고, CLI `--json`, 공통 내부 진단이 같은 `EVID` ID를 가진다. CLI 기본 텍스트에는 ID가 없고 같은 문제의 설명·claim ID를 보여준다. 쓰기는 `written`을 유지하며 기존 `warnings: string[]`와 인덱스·자동 커밋 계약은 그대로다.
- 기존 근거 필드 없는 페이지와 새 빈 위키는 `lint --strict`에서 새 진단으로 실패하지 않는다. 기존 lint 문제는 이전처럼 실패한다. 텍스트·JSON 각각에서 `--strict` 유무의 stdout은 동일하며 JSON에는 기존 문제의 ID를 새로 붙이지 않는다.
- `wiki_search` 결과는 기존 필드와 `usage_terms`를 보존한다. 요약은 최대 3개 claim·종류별 4개·한계 200 rune를 지키고 초과를 `truncated`로 표시한다. 읽기·YAML 파싱 불가 페이지, EVID001 기록, EVID002 또는 EVID003이 있는 v1 기록은 `unavailable`과 이유로 표시하고 나머지 결과를 유지한다. EVID004·EVID005·EVID007만 있는 v1 기록은 `recorded`다. 검색에서는 ref를 해결하지 않고 외부 데이터에 접근하지 않는다.
- 현재 위키·기본 스키마 양쪽에서 기존 2,000 rune 처리와 전문 링크 동작을 유지하고, 서버 소유 지침이 스키마 부분의 길이와 무관하게 MCP 초기화 instructions에 나타나는지 시험한다. 기존 위키 `_schema.md` 바이트는 바뀌지 않는다. 필드 왕복 파싱에서 한글·여러 출처·여러 공백·알 수 없는 enum을 시험한다. 동일 파일의 쓰기 경고·CLI JSON·내부 진단 ID와 텍스트/JSON lint 출력 순서를 반복 실행으로 비교한다.
- 누락 원문, 코드 발견, 재현 실패는 어떤 경로에서도 `fulltext` 또는 `replication` 완료로 자동 승격되지 않는다.

## 구현자가 확인할 파일

`SPEC.md` §8.3/§8.5/§9.12/§11, `DESIGN.md` MCP·스키마 경계, `internal/mcp/server.go`, `internal/mcp/tools.go`, `cmd/cogvault/lint.go`, `internal/terms/terms.go`(미확인 상태 보존 선례), 해당 테스트 파일. `internal/schema/default_schema.md`는 기존 지침과의 관계를 읽어 확인하되 이번 구현의 수정 대상은 아니다. 이 문서의 형식은 제안이며, 기존 계약과 충돌하면 정본을 갱신하고 그 변경을 검증한 뒤 구현한다.
