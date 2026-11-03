# 심층 리서치 자료 아카이브와 비PDF ingest 계획

> 상태: 비정본 구현 계획. 사용자 선택은 **아카이브 + 비PDF ingest**다. 아래 "승인된 운영 기본 정책"은 사용자가 채택한 기본값이며 ADR 0027에 **approved default**로 표시했다. 기본값 승인은 배포 설정 완료가 아니고 구현도 아니다. 그 밖의 미결정 정책은 승인된 선택으로 취급하지 않는다. ADR 0027 전체는 남은 질문 때문에 proposed다. 구현 시 `SPEC.md`·`DESIGN.md`·ADR의 계약과 근거는 저장소 규칙에 따라 영어로 작성한다.

## 목표와 현재 동작

Claude·ChatGPT 심층 리서치와 웹 검색에서 발견한 자료를 보관하고 ingest와 연결한다. 검색 스니펫·인용·리서치 보고서와 **출처 원문**을 구분한다. 아카이브는 Cogvault의 선택적 모듈이며 CLI/MCP/에이전트 스킬이 공통 캡처 핵심을 사용하게 한다. 범용 애드온 프레임워크는 만들지 않는다. 기존 PDF 동작은 유지한다.

- `internal/ingest/ingest.go`의 Claude `FileInput` 경로는 이미 `.md`·`.markdown`을 ingest한다. 비PDF 공백은 주로 OpenAI `TextInput`이다. `internal/config/config.go`의 OpenAI 소스 타입 검증은 PDF만 허용하고 ingest runner는 `PDFExtractor`로 고정된다. `.txt`는 현재 지원된다고 가정하지 않는다. v1은 OpenAI에 UTF-8 `.txt` 추출을 추가하고, Claude `.txt`는 설정·스캔·FileInput까지 별도 명시·시험 후 결정한다.
- PDF 추출은 `max_input_chars` 초과 시 절단하지 않고 실패한다(`internal/extract/pdf.go`). 새 Markdown·텍스트 추출도 같은 **완전 입력 또는 실패** 규칙을 적용한다.
- `cmd/cogvault/fetch.go`는 HTTP 텍스트를 `formatFetchedContent`로 가공해 frontmatter가 붙은 `.md`로 저장한다. HTML도 이 경로에서는 `.md`다. `LimitReader(..., 10<<20)`은 초과 응답을 조용히 자를 수 있다. fetch 파일의 해시와 HTTP 원본의 해시는 다르므로 원본 보존과 fetch 통합을 별도 설계한다.
- `ingest_ledger`의 키는 `(source_path, content_hash)`이고 Mac 절대경로를 사용한다. 서로 다른 경로의 같은 바이트도 별도 ledger 항목이다. `docs/deployment/nas.md`에 문서화된 NAS MCP/wiki/재생성 인덱스와 Mac LLM/PDF/로컬 ledger 배치를 전제로 검토하되 실서버 현황으로 단정하지 않는다. SQLite WAL DB는 SMB에 두지 않는다.
- 현재 MCP 쓰기는 `wiki_dir` 경계 안에 있다. 별도 캡처 쓰기 표면을 추가하려면 권한·저장 루트 예외를 ADR로 정해야 한다. 기존 `wiki_*` 경계와 `sources[]` 읽기 전용 ingest 입력은 암묵적으로 바꾸지 않는다.

## 캡처와 근거 연결

원본 바이트, 파생 텍스트, 보고서, 인용 메타데이터를 분리한다. 원본의 크기·SHA-256·획득 맥락을 기록하고 변경 불가로 보존한다. 파생 텍스트에는 원본 ID·추출기·버전·옵션을 연결한다. 크기 초과는 명시적 거부나 일부/실패 기록으로 처리하며 완전한 원본처럼 조용히 절단하지 않는다. 기존 fetch의 frontmatter·이용조건 기록은 유지할 가치가 있지만 원본 바이트로 간주하지 않는다.

레지스트리는 검색 인덱스와 달리 지속·백업 대상이다. 최소 기록은 안정적 `capture_id`, URL/제공 경로, 알려진 출처 신원·판본, 획득 시각, 매체 유형, 바이트 길이·해시, 완전성 판단과 근거, 원본/파생물 객체 ID, 인용·보고서 관계, 복구 이력·다음 행동이다. 호스트 경로를 영속 ID로 쓰지 않는다. 동일 바이트의 재요청은 원본 저장을 멱등 처리하되 **서로 다른 URL·제공자·인용·판본 관계를 보존**한다. 해시 일치만으로 같은 출처·같은 판본이라 판단하지 않는다. `sources[]`에 쓰기 전에 캡처 계층에서 중복을 판단하고, 실체화 시 `capture_id ↔ Mac source_path` 대응을 남긴다. 다른 경로의 같은 바이트가 ledger에서는 별도라는 사실도 보존한다.

`SPEC.md` §11.1의 `research_evidence.claims[].sources[].ref`는 위키 루트 상대 `.md` 경로다. 연결은 `capture_id → 출처 wiki 페이지 → claim source ref`이며(승인된 기본값), 출처 페이지가 생성되기 전에는 ref가 해결된 것으로 표시하지 않는다. 새 판본이 이전 판본을 자동으로 대체하지 않는다. capture ID를 ref에 직접 넣는 변경은 기존 계약 변경이므로 ADR에서 따로 결정하지 않는 한 하지 않는다. 보고서나 인용만으로 원문 확보를 주장하지 않는다.

레지스트리가 영속적으로 소유할 처리 상태는 **원본 확보**(없음/일부/확보/불명), **원본 완전성**(검증됨/불명/불완전), **추출**(대기/성공/일부/실패)과 복구 이력이다. Mac ingest 성공/실패의 권위는 Mac ledger에 있고 NAS 검색 상태는 재생성 가능한 인덱스를 조회해 안다. 레지스트리에 중복된 권위의 `ingested`·`indexed` 불리언을 저장하지 않는다. 상태 응답은 Mac receipt/ledger의 최신 관찰과 NAS 인덱스 조회를 출처·조회 시각과 함께 조합할 수 있다. Mac 오프라인의 `awaiting_ingest`는 계산된 대기 표시이며 성공 추정이 아니다.

## 승인된 운영 기본 정책 (프로토타입 구현, 운영 미배포, 배포 설정 별도)

사용자가 채택한 기본값이다. 근거와 영어 계약은 ADR 0027 item 1·2·7–11·13·14에 있다.

| 영역 | 기본값 | 아직 비어 있는 배포 설정 |
| --- | --- | --- |
| 저장소 | wiki와 분리된 NAS `archive/`, 별도 영속 볼륨. 검색 인덱스 재생성과 무관 | 실제 root·볼륨 |
| registry | 컨테이너만 여는 로컬 영속 볼륨의 SQLite. SMB 위 WAL 금지, 호스트 간 공유 SQLite 없음 | 실제 경로 |
| 백업 | 원본과 일관된 registry 사본(SQLite backup API 또는 `VACUUM INTO`)을 매일 함께 백업. NAS 밖 사본 1개 이상. **백업 사본 보존 30일** | 대상·방법·일정 확인·권한·검증·복원 시험 |
| 원본 보존 | 원본·판본·capture 기록은 자동 만료 없음 | — |
| 삭제 요청 | 종속성·영향 보고 → 격리(최소 30일 복구 가능) → 영구 purge는 건별 별도 확인 전 금지. 일괄·예약 purge 없음. 로컬 관리자 경로만, MCP 불가 | 보고 형식·격리 위치·확인 절차·purge 범위 |
| 삭제 기록 | 격리·복구·purge마다 원본 내용 없는 durable 삭제 기록(tombstone, seq 순서). 복원 시 최신 삭제 기록을 다시 적용해 삭제된 원본 부활 방지 | NAS 밖 삭제 기록 replica 위치, 삭제마다 replica를 최신으로 유지하는 방법, NAS 손실 시 복원이 replica를 읽는 경로 |
| MCP 경계 | 별도 캡처 표면, 기본 off. 상태 읽기·캡처 추가만. 삭제·덮어쓰기·임의 경로 접근 없음. `wiki_*` root와 `sources[]` 불변 | 권한 모델·credential·scope·담당자, enable 결정 |
| 인계 | 첫 버전은 manifest/receipt 파일. 공유 SQLite·새 네트워크 API 없음. Mac 오프라인이면 대기. ingest 권위는 receipt로 관찰한 Mac ledger(관찰 시각 포함), NAS 검색은 인덱스 조회 | 전송 마운트·경로·일정 |
| 불완전 자료 | `unknown`·`partial` 원본은 보존하되 자동 ingest 보류. 완성된 요약문은 원본이 아닌 summary로 처리 가능하며 원본 확보·완전성을 바꾸지 않음. 출처 복구 provenance 유지 | 보류 해제 절차·출처 페이지 표시 |
| 인용 | `capture → 출처 wiki 페이지 → 기존 ref`. 판본 자동 대체 없음 | — |

세 시간 기준을 섞지 않는다.

- 백업 30일은 **백업 사본**을 얼마나 두는지다. 원본 보존 기간이 아니다.
- 원본은 나이로 만료되지 않는다.
- 격리 30일은 복구 가능 기간이며, 끝나도 자동 purge하지 않고 purge 대상 자격만 생긴다.
- 삭제 후 모든 사본이 정확히 30일 뒤 사라진다고 보장하지 않는다. 각 백업 도구의 만료와 통제 밖 사본에 달려 있다.

**운영 활성화 차단(ADR item 14):** 실제 archive root·볼륨, NAS 밖 백업 대상·매일 일정 확인·삭제 기록 재적용을 포함한 복원 시험 통과, NAS 밖 삭제 기록 replica와 그 replica를 쓴 복원 시험, 권한·credential·scope 담당자, 인계 전송 마운트·경로 중 하나라도 비어 있으면 운영 캡처를 켜지 않는다. 임시 root에서 live wiki·NAS·설정·자격 증명 없이 하는 격리 프로토타입 개발·시험은 허용한다.

프로토타입은 이 차단을 `archive.deployment` 설정의 **운영자 제출 attestation 존재 검사**로만 강제한다: 모든 항목 존재, 복원 시험 보고서 통과·`backup_target` 아래 백업 대상·`deletion_log_replica` 사용, 활성화 시점에 replica가 live 로그 전체를 포함. 코드는 대상·replica가 실제로 NAS 밖인지, 백업이 매일 도는지, replica가 계속 최신인지, 권한·마운트가 맞는지, 보고서가 최신인지 확인하지 못한다. 검사 통과는 배포 승인이 아니며 운영 enable에는 별도 사람 검토가 필요하다. 어떤 설정 키로도 이 검사를 건너뛸 수 없다.

## 프로토타입 구현 현황 (`proto/research-archive`, PR #59, 미배포)

ADR 0027은 proposed 그대로이고 운영 경로·NAS·live wiki·자격 증명은 건드리지 않았다. 계약은 `SPEC.md` §3.1·§9.3·§9.16, 구조는 `DESIGN.md` §2.14.

- 구현: A 통합, registry v5(v1~v4 이전; v3의 기존 연결은 열 때 receipt 번호 규칙으로 basis를 고친다; 같은 보류·결과 capture 쌍의 연결은 한 번만), summary, 복구 시도, manifest/pickup/receipt 인계(객체당 확장자 고정, backend별 타입, 읽기 전용 ledger reader), 기본 off MCP 도구 5개와 gate, SIGHUP reload, 영향 보고·격리·복구·건별 purge·seq 삭제 기록, archive lock, 백업·검증·복원·복원 시험, 운영 활성화 attestation 검사.
- 리뷰 반영(PR #59): 여러 원본이 공유하는 파생 텍스트는 다른 원본이 필요로 하면 격리·purge하지 않는다. 복구는 크기·해시가 맞는 파일이 있을 때만 기록한다. 두 위치에 파일이 있으면 검증된 사본을 남긴다. 삭제 기록 한 줄이 로그 한도를 넘으면 쓰기 전에 거부한다. FIFO·비정규 파일은 막히지 않고 거부한다. capture map 갱신은 대상 디렉터리 flock으로 직렬화하고 16 MiB를 넘으면 거부한다. 텍스트 소스 읽기는 상한·no-follow로 바꿨다. 실패한 추출을 다시 요청하면 기록된 실패를 오류로 돌려준다.
- 추가 구현(미결을 정하지 않는 범위): 보류 capture의 운영자 재평가(`reassess`: 같은 바이트·같은 provenance의 verified capture를 새로 만들고 attempt로 연결, basis는 운영자 진술이며 cogvault가 독립 검증하지 않음), 기존 fetch `.md`의 로컬 반입(`import-fetched`: 바이트 그대로 재인코딩될 때만 본문을 형식 불명 `.response` 원본으로, fetch 파일은 derived 객체로 보존, 삭제 시 함께 처리), `archive_status`의 `evidence_ref_path_exists`(ref 경로에 파일이 있는지만; 페이지가 그 capture를 기술한다는 뜻 아님).
- E 단계 추가 구현(미결을 정하지 않는 범위):
  - capture map: Pickup이 source 디렉터리에 `.cogvault-archive-captures.json`을 원자적으로 기록한다. 신뢰 범위는 source 디렉터리와 같다.
  - ingest: 바이트 해시가 map의 object ID와 같을 때만 `archive_capture_ids`·`archive_object_id`를 코드로 기록하고, 모델이 쓴 값은 항상 지운다. 이 키는 digest 당시의 snapshot이고 전체 목록이 아니다. ingest는 변경 없는 success 행의 페이지를 다시 쓰지 않는다.
  - capture↔페이지 연결: archive registry의 `page_links`가 권위다. 수집한 receipt가 ledger success와 위키 페이지를 관찰할 때만 기록하고, capture마다 따로 기록한다(같은 바이트의 나중 capture도 자기 basis receipt를 가진다). 위키 페이지는 고치지 않는다. ingest 결과의 권위는 Mac ledger로 남는다. 추가만 하며, 나중 receipt가 다른 결과를 보면 `current`가 false가 된다. 이동한 페이지는 따라가지 않는다. `archive page-links <page>`, MCP `archive_page_links`로 조회한다.
  - receipt 순서: capture별 receipt 번호(`<capture>.<n>.json`의 n)만 쓴다. Mac 시계(`observed_at`)는 거꾸로 갈 수 있고 파일명은 `.10`이 `.2`보다 앞에 정렬되므로 쓰지 않는다. 연결의 basis는 가장 작은 번호, 최신 상태는 가장 큰 번호다. 수집·v2 backfill 모두 같은 규칙이다.
  - `archive_status`의 `page_frontmatter_lists_capture`: 페이지가 그렇게 주장한다는 뜻이다. `wiki_write`로 바꿀 수 있으므로 독립 검증이 아니다. MCP의 `ingest.evidence_ref`와 링크의 `evidence_ref`는 같은 규칙이다: 지금 그 경로에 파일이 있을 때만 나온다. `ingest.wiki_page`는 확인하지 않은 ledger 경로다. CLI status는 이를 `ref candidate`로 표시한다.
  - `archive extract`: `html-text/1`, 표준 라이브러리만 쓰고 스크립트·리소스 로드가 없다. 실패를 기록하고, 파생 텍스트는 인계하지 않는다. 출처와 함께 삭제된다.
  - `archive acquire [--recover]`: 운영자 전용이다. 모든 DNS 응답이 공개 주소여야 하고, 검사한 IP로만 연결한다. query·fragment·userinfo는 모든 redirect에서 거부한다. 완전성은 항상 unknown이다.
- 미구현: NAS 볼륨 배치, `cogvault fetch` 자체의 archive 연동(item 5), verified 판단 없는 unknown/partial 해제(미결 5), 자동·예약 재확보, robots·약관 처리, 비 UTF-8 charset 해석, 파생 텍스트 인계, 백업 일정·30일 만료, replica 최신 유지, oauth scope 부족 403 시험(bearer 401만 시험).
- 미결을 임시로 정한 프로토타입 값(미결 유지): MCP 텍스트 1 MiB, 드레인 30초, 비활성 시 상태 도구도 숨김, stdio reload 없음, URL query 전부 거부, 획득 32 MiB·60초.

## 단계와 의존성

| 단계 | 범위 | 선행 |
| --- | --- | --- |
| A — 독립 개선 | OpenAI `TextInput`에 `.md`·`.markdown`·UTF-8 `.txt` 추출/디스패치와 타입 검증. `max_input_chars` 초과 실패, PDF 유지. fetch 10 MiB 초과 명시적 거부 | 아카이브 ADR 불필요 |
| B — 경계 결정 | 운영 기본 정책은 승인됨(위 절). 남은 것: 배포 설정(root·볼륨·백업 대상·권한 담당·전송 마운트), 실제 권한·scope, 업로드 방식·최대 크기·접근 제한 자료, fetch 가공본/원본 관계, 보류 자료 해제 절차, 삭제 운영 세부, reload 운영 세부 | A와 병행 가능 |
| C — CLI 수동 반입 | 변경 불가 원본·레지스트리·복구 메타데이터, 수동 사용자 파일 반입과 상태 조회. 로컬 관리자용 삭제 요청·격리·복구·건별 purge와 삭제 기록. 업로드 전송 방식은 B에서 확정 | B(격리 프로토타입은 B 완료 전 가능) |
| D — MCP·교차 호스트 | 별도 캡처 인터페이스(기본 off), 에이전트 스킬, manifest+receipt 인계. 완료된 리서치 보고서 반입은 인용만 있는 기록과 원본 동봉 기록을 구분. 캡처 활성화의 명시적 reload(아래 절). 운영 활성화는 item 14 차단 조건 해소 후 | B, C |
| E — HTML·자동 재확보 | HTML 원본/파생 텍스트 분리와 안전한 추출, 공개 원문 재획득 및 복구 흐름. 인증 우회·임의 브라우저 세션 접근 제외 | C |

심층 리서치 환경에서 임의 파일 쓰기나 첨부 원본 접근을 할 수 있다고 가정하지 않는다. D는 완료된 보고서와 확보 가능한 자료를 명시적으로 전달받는 흐름이다. 기존 Mac PDF 원본과 `wiki_*` 경로 규칙은 유지한다.

## 불완전 자료 복구

최초 기록에 원인·시도·다음 행동을 남긴다. 원출처 URL, 확인된 발행처/저자 판본, 사용자 제공 파일을 가능한 경로로 평가한다.

| 원인 | 복구 경로 |
| --- | --- |
| 인용·스니펫만 있음 | 출처 신원 확인 후 공개 원문 확보 또는 사용자 파일 요청 |
| 응답 잘림·전송 오류 | 제한·응답 크기·해시 기록, 명시적 크기 정책으로 재시도; 실패본은 일부 |
| 인증·유료·접근 제한 | 권한 우회 없이 공개 판본이나 사용자 제공본 탐색, 사람 조치 대기 |
| 파일·쪽·부록 누락 | 목차·쪽수·첨부 목록 대조 후 누락 부분 요청 |
| 판본 불일치 | 날짜·식별자·내용 표지·해시 대조, 별도 캡처와 관계 기록 |
| 원본 확보, 추출/OCR 실패 | 원본 재획득 없이 추출만 재실행 |

완전성은 선언된 길이, 파일 무결성, 페이지/절 구성, 첨부 목록 등 판정 가능한 근거가 있을 때만 `verified`다. 요약보다 길거나 모델이 누락 문장을 복원했다는 이유로 승격하지 않는다. 판정 불가면 `unknown`이다. 이전 일부 캡처·보고서·인용을 덮어쓰지 않고 새 캡처와 시도 결과를 연결한다. 재시도에는 원인별 상한과 재개 조건을 두며 무한 폴링·자격 증명 저장은 하지 않는다. 접근 제한은 `human_action_required`, 일시 장애와 영구 실패는 구분한다.

새 원본을 확보하면 파생 텍스트를 다시 추출한다. `unknown`·`partial` 원본은 보존하되 자동 ingest를 보류한다(승인된 기본값). 보류 해제 절차와, 해제된 일부 자료의 한계를 wiki 출처 페이지에 전달하는 방식은 미결이다. 완성된 요약문은 summary로만 다루며 원본으로 표시하지 않는다. ingest는 Mac ledger/receipt로, NAS 검색 가능성은 인덱스 조회로 확인한다. 원문 상태 변경만으로 downstream 성공을 선언하지 않는다.

## 캡처 활성화 reload (사용자 승인 설계, 프로토타입 구현, 운영 미배포)

사용자가 승인한 설계 범위다. 계약 본문은 ADR 0027 item 12이고 이 절은 구현 요구와 시험을 정의한다. 프로토타입은 `archive` 설정, 기본 off MCP 캡처 도구, SIGHUP reload를 구현했다(위 현황 절). 아래 "구현 전 확인한 동작"은 설계 근거로 남긴 기록이다.

구현 전 확인한 동작(mcp-go v0.47.0):

- `config.Load`는 `KnownFields(true)` 엄격 디코드 → 기본값 → `validate()` 순서다. 알 수 없는 키는 거부하므로 `archive` 절을 추가하기 전 바이너리는 그 설정을 읽지 못한다.
- `runServe`는 시작 시 설정을 한 번 읽고 `cogmcp.NewServer`로 도구 7개를 등록한다. `serveListener`는 `SIGINT`·`SIGTERM`만 받는다. 처리하지 않는 `SIGHUP`은 macOS에서 프로세스를 끝내고(launchd가 재시작) 컨테이너 PID 1에서는 무시된다. 이미지는 exec 형식 `ENTRYPOINT`로 cogvault를 PID 1로 실행한다.
- `AddTool`은 `WithToolCapabilities`가 없으면 `tools.listChanged: true`를 암묵 설정하고 `initialize` 응답에 이미 광고된다. `AddTools`·`DeleteTools`는 초기화된 등록 세션 전체에 `notifications/tools/list_changed`를 비차단으로 넣는다(채널 100개, 가득 차면 버림). streamable HTTP는 GET 수신 스트림이나 다음 POST 응답(SSE로 승격)으로 전달한다. 임시 probe로 두 경로를 관찰했고 probe는 삭제했다. `SetTools`는 맵을 비운 뒤 추가하므로 동시 `tools/list`가 빈 목록을 볼 수 있다. `tools/call`은 잠금 안에서 핸들러를 복사하고 잠금 밖에서 호출하므로 `DeleteTools` 직전에 조회된 호출은 계속 실행된다.
- 클라이언트가 알림을 받고 목록을 새로 고친다는 보장은 없다. Claude·ChatGPT 커넥터의 갱신 동작은 확인하지 않았다. 따라서 도구 목록은 안내이고 경계는 핸들러 안의 게이트다.

구현 요구:

1. 트리거(권고): 실행 중인 `cogvault serve`에 대한 `SIGHUP`. 프로세스에 신호를 보낼 수 있는 로컬 관리자만 시작한다. MCP 도구·HTTP 경로·파일 감시로는 reload하지 않는다. CLI 래퍼는 별도 로컬 제어 채널(PID 파일·Unix 소켓)이 필요하므로 미결(ADR 미결 8).
2. 시작 시 확정한 같은 설정 경로를 `config.Load`로 전부 파싱·검증한다. 실패하면 이전 상태를 유지하고 오류를 기록한다.
3. 적용 값은 캡처 활성화 boolean 하나(`archive.capture_enabled`, 기본 `false`). 활성 상태는 gate의 open 값이며 호출마다 gate 잠금 아래에서 읽는다. reload는 직렬 실행하고 진행 중 도착한 신호는 한 번의 추가 reload로 합친다.
4. 재시작 전용: archive root, registry DB, `wiki_dir`, `db_path`, `auth` 전체(mode·scope·issuer·audience·자격 증명 출처), 업로드·크기 정책. 이 값이 실행 중 snapshot과 다르면 reload 전체를 거부하는 것이 후보안이다(키 이름만 기록, 값은 기록하지 않음). 그 밖의 키(`llm`, `git` 등)는 적용하지 않고 재시작 필요를 기록한다.
5. 시작 시 archive root가 설정돼 있으면 비활성 상태에서도 archive를 열어 검사한다. 시작 시 설정이 없으면 활성화 reload를 거부한다.
6. 비활성화 순서: 게이트 닫기 → `DeleteTools` → 진행 중 캡처 호출 드레인. 닫힌 뒤 새 호출은 도구 오류를 받고 아무것도 저장하지 않는다. 진행 중 저장은 reload가 취소하지 않는다. 드레인 상한과 초과 시 기록 내용은 미결.
7. 활성화 순서: 게이트 열기 → `AddTools`. 인증·권한은 바꾸지 않는다. 각 캡처 호출은 기존 `httpauth` 미들웨어(서버 전체 mode·`RequiredScopes`)를 독립적으로 통과한 뒤 게이트를 통과한다. 도구별 scope는 ADR 미결 3.
8. `SetTools`를 쓰지 않는다. 알림을 권한 경계로 가정하지 않는다.
9. reload마다 트리거·결과·이전/새 값·거부 사유를 기록한다. 자격 증명은 기록하지 않는다.
10. `sessionIdleTTLFor` 주석의 "서버 알림 없음" 전제를 재검토하고 갱신한다. `SPEC.md`(설정 키, serve 신호, 캡처 도구)·`DESIGN.md`·운영 문서는 구현과 함께 갱신한다.

시험(구현됨; `cmd/cogvault/archive_serve_test.go`, `internal/mcp/archive_tools_test.go`, `internal/config/archive_test.go`):

- 설정: `archive` 절 파싱, 활성화 기본값 `false`, 알 수 없는 하위 키 거부.
- reload 거부: 잘못된 YAML, 검증 오류, 재시작 전용 값 변경 → 상태 불변, 오류 기록, 캡처 호출 결과 불변.
- reload 적용: 유효한 토글 → snapshot 교체, 다른 키 변경은 미적용 기록.
- 게이트: 비활성 상태 캡처 호출 → 도구 오류, 객체·레지스트리 행 0개. 활성화 후 같은 호출 성공.
- 드레인: 진행 중 호출을 gate에 들인 채 비활성화 → 새 호출 즉시 거부, reload는 그 호출이 끝난 뒤 반환, shutdown 취소 시 대기 중단. 실제 저장을 멈추는 테스트 훅은 쓰지 않았고, 진행 중 저장 완료는 gate 단위 시험과 동시 토글 `-race` 시험으로만 확인했다.
- 오래된 목록: 비활성화 후 이름으로 직접 `tools/call` → 오류·쓰기 없음. `DeleteTools` 전에 조회된 핸들러도 게이트에서 막힘.
- 목록·알림: streamable HTTP `httptest`에서 활성화 후 `tools/list`에 포함, GET 스트림에서 `list_changed` 관찰. 알림 수신은 갱신 보장이 아님을 시험 이름·주석에 명시.
- 인증: bearer 모드에서 토큰 없는·잘못된 토큰 캡처 호출 401, 올바른 토큰은 도구 도달. 활성화가 이 결과를 바꾸지 않음. oauth scope 부족 403은 아직 시험하지 않았다.
- 신호: `serveListener`가 `SIGHUP`으로 reload 콜백을 부르고 서비스를 계속하며, `SIGTERM`은 기존처럼 종료.
- 표면: 어떤 설정에서도 reload·관리 MCP 도구가 목록에 없음.
- `-race`로 동시 캡처 호출과 토글 반복.

로컬 프로토타입: 처음 평가에서는 미결 3·6·8 확정 뒤로 미뤘으나, 사용자 승인으로 미결을 열어 둔 채 구현했다. 미결은 위 현황 절의 임시 값으로만 정했고 결정으로 취급하지 않는다.

## 수용 검증과 남은 결정

- A: OpenAI Markdown·UTF-8 텍스트가 완전 추출 후 ingest되고 길이 초과는 실패한다. PDF 기존 동작과 Claude `.md`·`.markdown`은 유지된다. Claude `.txt`는 지원 여부를 별도 명시·시험한다. fetch 10 MiB 초과는 조용히 잘리지 않는다.
- C: 같은 원본 바이트 재전송은 중복 저장되지 않지만 서로 다른 출처 관계는 유지된다. 다른 판본은 별도 ID다. `capture_id ↔ source_path` 실체화가 추적되고 다른 Mac 경로의 같은 바이트는 ledger의 별도 키임을 확인한다.
- C/E: 스니펫만 있으면 완전성은 미확인이다. 원본은 있으나 추출 실패면 재추출만 한다. 이전 일부 캡처와 복구 이력이 남고 판본·완전성 증거가 부족하면 `unknown`이다. 크기·입력 길이 초과를 완전 캡처/성공 ingest로 표시하지 않는다.
- D: Mac 오프라인에는 캡처와 대기 사유가 남는다. Mac ledger/receipt 없이 ingest 성공이라 말하지 않고 NAS 인덱스는 조회로 확인한다. claim ref는 출처 wiki 페이지가 생겨야 해결된다. `unknown`·`partial` 원본은 manifest로 자동 인계되지 않는다. 새 판본이 기존 ref 대상을 바꾸지 않는다.
- D: 캡처 MCP 표면은 기본 off이고, 켜져도 삭제·덮어쓰기·호스트 경로 입력이 없으며 `wiki_*` root는 그대로다. item 14 attestation 검사를 통과하지 못하면 활성화가 거부된다. 통과는 배포 승인이 아니다.
- B/C: 레지스트리·원본·복구 이력은 검색 인덱스 재생성과 독립적이다. 백업은 원본과 일관된 registry 사본을 함께 담는다. 복원 시험은 (1) 백업 시점 상태 복구, (2) 백업 이후 격리·purge된 원본이 삭제 기록 재적용으로 다시 나타나지 않음을 확인한다.
- C: 삭제 요청은 먼저 종속성·영향을 보고한다. 격리된 원본은 캡처·상태·인계에서 빠지고 복구할 수 있다. 격리 30일이 지나도 자동 purge되지 않는다. purge는 건별 확인 없이는 실행되지 않는다. MCP로는 삭제할 수 없다.

남은 결정(설계 선택이며 승인된 정책이 아니다): 배포 설정(실제 root·볼륨, NAS 밖 백업 대상·방법·일정 확인·권한·검증, 권한·credential·scope 담당, 인계 전송 마운트·경로·일정), NAS 밖 삭제 기록 replica 위치와 최신 유지 방법, 삭제 운영 세부(영향 보고 형식·격리 위치·purge 확인 절차·purge 범위), 실제 MCP 권한·scope와 enable 결정, 수동 파일 업로드 방식, 최대 크기·접근 제한 자료 취급, fetch/원본 관계와 출처 페이지 `capture_id`, 보류 자료 해제와 summary 표시, reload 운영 세부(트리거 확정·재시작 전용 값 변경 처리·드레인 상한·stdio 지원). 위 "승인된 운영 기본 정책"과 reload의 범위·경계는 사용자가 승인했다.
