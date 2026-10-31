# Source in a dedicated interactive Bash pane AFTER setting its role title.
# The handler reads one POP result JSON from stdin, then the complete archive.
if [[ $- != *i* || -z ${BASH_VERSION:-} ]]; then
  printf '%s\n' 'Postman requires interactive Bash.' >&2
  return 1
fi
for _postman_path in POSTMAN_BIN POSTMAN_CONFIG POSTMAN_HANDLER; do
  if [[ ${!_postman_path:-} != /* ]]; then
    printf '%s must be an absolute path\n' "$_postman_path" >&2
    return 1
  fi
done
unset _postman_path
if [[ ! -x $POSTMAN_BIN || ! -x $POSTMAN_HANDLER || ! -r $POSTMAN_CONFIG ]]; then
  printf '%s\n' 'Postman binary/handler must be executable and topology readable.' >&2
  return 1
fi
set -o emacs
_postman_receive() {
  local result
  # One POP per cue. READLINE_LINE and READLINE_POINT remain untouched.
  result=$("$POSTMAN_BIN" pop --postman-md "$POSTMAN_CONFIG") || return
  printf '%s\n' "$result" | "$POSTMAN_HANDLER"
}
bind -x '"\C-x\C-p":_postman_receive'
"$POSTMAN_BIN" register-notifier --postman-md "$POSTMAN_CONFIG"
