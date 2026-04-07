package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/storage"
)

const defaultSchemaContent = `# Wiki Schema

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
1. wiki_list("<wiki_dir>/")로 최상위 구조 확인 (기본값 예: ` + "`" + `"_wiki/"` + "`" + `)
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
`

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize a vault with config, wiki directory, schema, and database",
		RunE:  runInit,
	}
}

func runInit(cmd *cobra.Command, args []string) error {
	vaultRoot, err := resolveVaultRoot(cmd)
	if err != nil {
		return err
	}

	// 1. Config file
	if err := config.Save(vaultRoot); err != nil {
		return fmt.Errorf("init config: %w", err)
	}

	cfg, err := config.Load(vaultRoot)
	if err != nil {
		return fmt.Errorf("init load config: %w", err)
	}

	// 2. Wiki directory
	wikiAbs := filepath.Join(vaultRoot, cfg.WikiDir)
	if err := os.MkdirAll(wikiAbs, 0o755); err != nil {
		return fmt.Errorf("init wiki dir: %w", err)
	}

	// 3. Schema file (via storage for symlink/traversal security)
	fsStore := storage.NewFSStorage(vaultRoot, cfg)
	if err := fsStore.WriteSchema([]byte(defaultSchemaContent)); err != nil {
		return fmt.Errorf("init schema: %w", err)
	}

	// 4. Adapter
	adpt, err := newAdapter(cfg.Adapter)
	if err != nil {
		return err
	}

	// 5. Database
	dbAbs := filepath.Join(vaultRoot, cfg.DBPath)
	dbExisted := fileExists(dbAbs)

	idx, err := index.NewSQLiteIndex(vaultRoot, dbAbs, cfg)
	if err != nil {
		return fmt.Errorf("init database: %w", err)
	}
	defer idx.Close()

	if dbExisted {
		_, _, _, ccErr := idx.CheckConsistency(fsStore, adpt, true)
		if ccErr != nil {
			if errors.Is(ccErr, index.ErrConsistencySystemic) {
				return fmt.Errorf("init consistency check: %w", ccErr)
			}
			cmd.PrintErrln("warning: some files had errors during consistency check:", ccErr)
		}
	} else {
		if err := idx.Rebuild(fsStore, adpt); err != nil {
			return fmt.Errorf("init rebuild index: %w", err)
		}
	}

	cmd.Println("Initialized vault at", vaultRoot)
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
