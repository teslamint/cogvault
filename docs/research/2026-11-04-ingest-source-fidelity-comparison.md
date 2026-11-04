# Isolated source-fidelity comparison (2026-11-04)

## Setup

- Inputs: a five-page Korean blog PDF (`hoch.io`, the reported regression) and a ten-page English public engineering article PDF (`anthropic.com`). Copies of both PDFs were placed in one temporary input directory. No URL was fetched and no source was sent to an external service.
- Baseline: installed `cogvault v0.3.0`, whose build info reports Git revision `761516e` and `vcs.modified=true`. Its exact uncommitted build contents cannot be reconstructed from the revision alone.
- Improved: this branch, built locally. Both runs used the same local Strata `iq3_s` endpoint, source copies, user-managed `_schema.md` snapshot, and LLM settings. Each condition had a separate temporary wiki and SQLite DB; the production wiki and ledger were not used.
- Method: one complete run per condition, sequential, measured with `/usr/bin/time -p`. A repeated intermediate prompt revision took 115.28 s and the final prompt before the final metadata-filter adjustment took 60.02 s, illustrating substantial run-to-run variation. No randomized order, cache reset, or repeated paired trial was performed.

## Observations

| Measure | Installed baseline | Final branch |
| --- | ---: | ---: |
| Successful digests | 2/2 | 2/2 |
| Total wall time | 213.82 s | 49.38 s |
| PDF annotation URLs recorded in Korean page | 0 | 10 distinct HTTP(S) destinations |
| PDF annotation URLs recorded in English page | 0 | 21 distinct HTTP(S) destinations |
| PDF-declared title recorded separately | No | Yes, both pages |
| Model-proposed canonical `url` accepted on new PDF page | Not observed | No |

The final run logged Korean-PDF extraction 87 ms, inference 21,489 ms, and post-processing 11 ms; English-PDF extraction 64 ms, inference 27,279 ms, and post-processing 4 ms. These are per-file stages, not the entire 49.38 s process. The baseline has no stage measurements. The large wall-time difference cannot be attributed to this change: local inference runtime state, warm caches, model output length, and stochastic generation were not controlled.

After that run, review added rejection of control characters in declared PDF metadata, whitespace/control characters in annotation URLs, and an input-mode label to timing logs. Those hardening changes were covered by local tests but not by another inference run; the measured samples did not contain such characters.

The structured URL retention is deterministic: the baseline pages had no PDF annotation fields; final pages contained the extracted destinations. None is asserted to be canonical or checked online. The Korean PDF has no declared PDF Author field, so `pdf_author` is absent even though the article names its author in body text. The final summary attributes claims to the author and includes the shadow-agent example, but it still omits the article's explicit personal-opinion/company disclaimer and its named Dynamic Workflow/Gemini example. The prompt improves the instruction, not guaranteed factual coverage. A human should consult the original for citation or detailed evidence.

## Limits

Two public-document examples and one baseline/final run do not establish a general quality or speed improvement. The installed baseline was a dirty build at the main revision, so it is not an exact clean-main control. The benchmark neither changed production data nor deployed the branch.
