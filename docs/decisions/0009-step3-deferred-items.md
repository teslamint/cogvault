# 0009-step3-deferred-items

Status: deferred
Date: 2026-04-06

## Context

Step 3 구현 및 리뷰에서 확인된 후속 단계 의무사항과 미해결 항목.

## Deferred Items

### Step 4 의무: exclude 규약 테스트

`adapter.Scan`의 exclude 파라미터는 타입으로 강제되지 않고 호출자 규약에 의존. CheckConsistency가 `cfg.AllExcluded()`를 전달하는지 테스트로 잠가야 함.

### Step 5 의무: root 팩토리 단일화

`root` (절대 경로)는 trusted input으로 설계됨. 현재는 문서 전제에 불과하며, Step 5 MCP 서버 생성 시 root 경로 생성을 단일 팩토리로 통합하여 코드로 강제해야 함.

### storage와 adapter의 path 유틸 통합

`internal/storage/fs.go`의 `resolvePath`, `containsDotDot`, `hasPathPrefix`와 `internal/adapter/pathutil.go`의 동일 로직이 별도 존재. v0.2에서 `internal/pathutil`로 추출 후보.

### TOCTOU ADR 범위

`docs/decisions/0004-storage-toctou.md`가 Storage만 명시. Adapter에도 동일 판단 적용됨을 0004에 추가하거나, 0008에서의 기록으로 충분한지 판단 필요.

### Markdown Links 판정 규칙 승격

현재 Markdown adapter의 외부 링크 판정 규칙(scheme, 절대경로, protocol-relative, heading-only, Windows 절대)은 코드와 계획 문서에만 존재. Markdown adapter가 fallback 이상이 되면 SPEC.md에 승격 필요.

## Revisit Triggers

- Step 4 구현 시작 시 (exclude 테스트)
- Step 5 구현 시작 시 (root 팩토리)
- v0.2 계획 시 (pathutil 통합)

## Related Files

- `internal/adapter/pathutil.go`
- `internal/storage/fs.go:143-174`
- `docs/decisions/0004-storage-toctou.md`
- `docs/decisions/0008-step3-adapter-decisions.md`
