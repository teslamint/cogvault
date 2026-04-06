# 0008-step3-adapter-decisions

Status: accepted
Date: 2026-04-06

## Context

Step 3 adapter implementation 후 내부 리뷰 + Codex 리뷰에서 계획 단계에서 확정되지 않았거나 구현 중 변경된 결정 사항이 확인됨.

## Decisions

### D1. 보안 함수는 adapter 패키지 레벨에서 공유

`ValidateRelPath`, `CheckSymlinks`, `HasPathPrefix`, `IsExcluded`, `ContainsDotDot`를 `internal/adapter/pathutil.go`로 추출. `ExtractTitle`, `ExtractAliases`, `ExtractFrontmatterTags`는 `internal/adapter/extract.go`로 추출.

초기 계획은 obsidian/markdown 각각에 로컬 복제였으나, 코드 리뷰에서 보안 함수 중복이 divergence 위험으로 판정되어 즉시 추출.

### D2. Scan은 파일 경로도 exclude 매칭

초기 구현은 `d.IsDir()` 분기에서만 exclude를 체크하여, `exclude: ["private/secret.md"]` 같은 파일 경로 제외가 동작하지 않았음. SPEC 3.2에서 `exclude ∪ exclude_read`는 "스캔 + 인덱싱 제외"로 정의하므로, 파일 경로도 매칭 대상.

수정: `.md` 확장자 + symlink 체크 후, `fn(rel)` 호출 전에 `adapter.IsExcluded(rel, exclude)` 추가.

### D3. Markdown `![alt](path)` → Attachments 분리

초기 구현은 `![alt](path)`도 Links에 저장했음. Obsidian adapter는 `![[file]]` → Attachments로 정확히 분리하므로, adapter 간 의미가 어긋남. Markdown regex를 `!?\[...\]\(...\)`로 변경하고 `!` prefix로 image 감지 → Attachments로 분류.

### D4. Markdown href TrimSpace 순서

`isExternalLink` 호출 전에 href를 TrimSpace하지 않으면 `( https://example.com )` 같은 입력이 외부 링크로 걸러지지 않음. TrimSpace → isExternalLink → # strip 순서로 수정.

### D5. Wikilink regex에서 줄바꿈 제외

`\[\[([^\]]+)\]\]`는 줄바꿈도 매칭하여 `[[foo\nbar]]`를 하나의 wikilink로 잡음. Obsidian은 줄바꿈 wikilink를 인식하지 않으므로 `[^\]\n]+`로 변경.

### D6. TOCTOU ADR 범위 확장

`docs/decisions/0004-storage-toctou.md`는 Storage만 명시하지만, Adapter의 `CheckSymlinks` → `ReadFile` 경쟁도 동일한 판단(single-user MVP에서 수용)이 적용됨. 별도 ADR로 분리하지 않고 여기에 기록.

## Alternatives Considered

- D1: v0.2에서 `internal/pathutil`로 storage와도 공유 → 현재는 adapter 내부 공유만. storage 리팩터링은 Step 3 범위 밖.
- D2: MCP handler에서 필터링 → Scan 자체가 정책을 모르면 모든 호출자가 후처리해야 함. Scan 내부에서 처리가 더 안전.
- D3: Attachments 없이 Links에 통합 → v0.3 graph/lint에서 image와 note 참조가 섞여 모든 소비자가 필터링해야 함.

## Revisit Triggers

- storage와 adapter의 path 유틸 통합 필요 시 → `internal/pathutil` 추출
- Markdown adapter가 fallback 이상의 역할을 갖게 되면 → Links/Attachments 판정 규칙을 SPEC에 승격
- TOCTOU 완화가 필요해지면 → `O_NOFOLLOW` 또는 fd-based Fstat 도입

## Related Files

- `internal/adapter/pathutil.go`, `internal/adapter/extract.go`
- `internal/adapter/obsidian/scanner.go`, `parser.go`
- `internal/adapter/markdown/parser.go`
- `docs/decisions/0004-storage-toctou.md`
