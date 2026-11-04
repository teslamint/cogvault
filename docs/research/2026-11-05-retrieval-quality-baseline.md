# Retrieval quality baseline (2026-11-05)

## Setup

The baseline uses a SQLite `.backup` snapshot of the local Cogvault index and a config that points `db_path` at that snapshot and `wiki_dir` at the mounted wiki. The benchmark config sets a long consistency interval so `cogvault search` reads the frozen index rather than reconciling it during each invocation. Search may read wiki files to render summaries, but does not write to the wiki: the CLI's consistency path scans/stats/reads/parses files and applies changes only to its configured index; result rendering reads files. The snapshot is the only configured database.

The search CLI accepts limits from 1 through 100; the benchmark requests 30. The index uses trigram FTS ranking for queries of at least three Unicode runes (`ORDER BY rank`) and path-ordered LIKE matching for shorter queries. The reported metrics therefore describe the current mixed search behavior, not a single ranking algorithm.

## Labeling rule

Search and read events come from the local `serve.err.log`. Slog field values are decoded from Go quoting before labeling or scoring, so surrounding quotes are not part of the query. Consecutive search events no more than 5 seconds apart form a burst. For each burst, a read event labels its page relevant to every query in that burst when it occurs after the burst's final search, no more than 120 seconds after that search, and before the next burst begins. Only pages under `sources/` or `synthesis/` are eligible; the schema file is excluded. Repeated query strings are deduplicated and their labels are unioned. Unique queries with no eligible following read are counted but excluded from scored metrics.

The complete log span observed was 2026-10-23 through 2026-10-31 (first search 2026-10-23 14:30:24; last search 2026-10-31 23:13:05). It contained 73 search calls in 35 bursts, producing 69 unique queries: 44 labeled and 25 without labels. The labeled set contains 153 query–page pairs across 38 distinct pages. Two labeled pages were absent from the snapshot index at the start of the benchmark.

## Results

Metrics are macro-averaged over the 44 labeled unique queries. Reciprocal rank is the inverse rank of the first relevant result (zero if absent); recall@k is the fraction of that query's relevant pages returned in the first k results. Zero-hit count is the number of labeled queries with no relevant page in the top 30.

| Run | MRR | Mean recall@5 | Mean recall@10 | Zero relevant in top 30 |
|---|---:|---:|---:|---:|
| 1 | 0.292005 | 0.233425 | 0.304258 | 16 / 44 |
| 2 | 0.292005 | 0.233425 | 0.304258 | 16 / 44 |
| 3 | 0.292005 | 0.233425 | 0.304258 | 16 / 44 |
| Mean | 0.292005 | 0.233425 | 0.304258 | 16 / 44 |
| Population variance | 0 | 0 | 0 | 0 |

All three runs returned identical per-query hits and aggregate values. Three labeled queries logged on 2026-10-31 were searched with the benchmark config and limit 30; each CLI hit list matched the corresponding run-1 hit list.

## Limits

This is implicit relevance, not human-judged relevance. Selection bias is optimistic: reads can only happen for surfaced pages, so unclicked relevant results remain unlabeled. Burst attribution is pessimistic per query: a page read is labeled relevant to every query in its burst, even if only one sibling query surfaced that page. The snapshot is from 2026-11-05 while the queries span 2026-10-23 through 2026-10-31, so index changes since then affect results; two labeled pages were not present in the snapshot index. The sample is small and comes from one user's search and reading behavior; the 120-second attribution window and 5-second burst threshold are operational choices, and changing either can change the labels.

## Rerun

From the repository root, after preserving the current index as the ignored snapshot and retaining the ignored dataset/config, run:

```sh
python3 .omc/self-improve/topics/retrieval-quality/bench/build_dataset.py
python3 .omc/self-improve/topics/retrieval-quality/bench/run.py --runs 3
```

The script writes per-run results, the initial snapshot-index coverage, and aggregate results under the same git-ignored topic directory. It invokes the installed `cogvault` CLI with a result limit of 30.
