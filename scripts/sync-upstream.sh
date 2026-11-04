#!/usr/bin/env bash
# 上游只作为「补丁源」，不是基线。
# 用法：scripts/sync-upstream.sh            查看上游有哪些新提交
#       scripts/sync-upstream.sh --merge    把 upstream/master 整段合进当前分支
set -euo pipefail

UPSTREAM_REMOTE="${UPSTREAM_REMOTE:-upstream}"
UPSTREAM_BRANCH="${UPSTREAM_BRANCH:-master}"
CURRENT="$(git rev-parse --abbrev-ref HEAD)"

if ! git remote get-url "$UPSTREAM_REMOTE" >/dev/null 2>&1; then
  echo "没有名为 $UPSTREAM_REMOTE 的远端。先执行："
  echo "  git remote add $UPSTREAM_REMOTE git@github.com:yusheng-g/agentwork.git"
  exit 1
fi

echo "==> fetch $UPSTREAM_REMOTE"
git fetch "$UPSTREAM_REMOTE" --prune

BASE="$(git merge-base HEAD "$UPSTREAM_REMOTE/$UPSTREAM_BRANCH" || true)"
if [ -z "$BASE" ]; then
  echo "当前分支与 $UPSTREAM_REMOTE/$UPSTREAM_BRANCH 没有共同祖先（独立历史），只能 cherry-pick 具体提交。"
  exit 0
fi

COUNT="$(git rev-list --count "$BASE..$UPSTREAM_REMOTE/$UPSTREAM_BRANCH")"
echo "==> 上游领先共同祖先 $COUNT 个提交（HEAD=$CURRENT）"
git log --oneline --no-merges "$BASE..$UPSTREAM_REMOTE/$UPSTREAM_BRANCH" | head -40

if [ "${1:-}" = "--merge" ]; then
  echo "==> merge $UPSTREAM_REMOTE/$UPSTREAM_BRANCH 到 $CURRENT"
  git merge --no-ff "$UPSTREAM_REMOTE/$UPSTREAM_BRANCH" -m "chore: sync upstream $UPSTREAM_BRANCH"
  echo "完成。冲突请在当前分支解决后提交。"
else
  cat <<'EOF'

下一步（二选一）：
  * 只摘需要的修复： git cherry-pick <sha> [<sha>...]
  * 整段同步：       scripts/sync-upstream.sh --merge
EOF
fi
