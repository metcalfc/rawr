# Sourced only in VHS's new terminal; never source into an existing Rex session.
# Keep the PATH explicit: gh and ssh cannot fall through to real providers.
export PATH="$RAWR_DEMO_ADAPTERS:$RAWR_DEMO_BIN:/usr/bin:/bin:/usr/sbin:/sbin"
export PS1='$ '
export HISTFILE=/dev/null
unset REX_SESSION REX_BLOCK REX_CLIENT RAWR_REX RAWR_SERVER REXAPPS_REX
unset EXE_HOST EXE_SSH_KEY SSH_AUTH_SOCK GH_TOKEN GITHUB_TOKEN GH_HOST
cd "$RAWR_DEMO_PROJECT"
clear
printf '\033[1;36mRAWR v0.2.0\033[0m  terminal UI tour\n\n'
printf 'Files + changes. Pull requests + issues. VM details.\n\n'
printf 'Real Go apps and a disposable local Git project.\n'
printf 'GitHub + VM provider data are fixtures.\n'
printf 'Native Rex panes: see the README screenshot.\n\n'

rawr_demo_outro() {
    clear
    printf '\033[1;36mRAWR v0.2.0\033[0m\n\n'
    printf 'Three tools. One shared rawrkit.\n\n'
    printf 'This tour: standalone UIs, fixture providers.\n'
    printf 'Native Rex panes: see the README screenshot.\n'
}
