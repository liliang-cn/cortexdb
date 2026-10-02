#!/usr/bin/env sh
# Writes docs/llms-full.txt: the README and the guide as one plain-text file,
# for language models that read a site's documentation in a single request.
# Run from the repository root; the Pages workflow runs it before every deploy.
set -eu
out=docs/llms-full.txt
{
  printf '# CortexDB — full documentation\n\n'
  printf '> Generated from README.md and docs/GUIDE.md at commit %s. Summary: https://liliang-cn.github.io/cortexdb/llms.txt\n\n' "$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
  cat README.md
  printf '\n\n---\n\n'
  cat docs/GUIDE.md
} > "$out"
echo "wrote $out ($(wc -c < "$out" | tr -d ' ') bytes)"
