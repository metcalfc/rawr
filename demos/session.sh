# Sourced only in VHS's new terminal; never source into an existing Rex session.
# Keep the PATH explicit: gh and ssh cannot fall through to real providers.
export PATH="$RAWR_DEMO_ADAPTERS:$RAWR_DEMO_BIN:/usr/bin:/bin:/usr/sbin:/sbin"
export PS1='$ '
export HISTFILE=/dev/null
unset REX_SESSION REX_BLOCK RAWR_REX RAWR_SERVER REXAPPS_REX
unset EXE_HOST EXE_SSH_KEY SSH_AUTH_SOCK GH_TOKEN GITHUB_TOKEN GH_HOST
cd /tmp
clear
printf '\033[1;36mRAWR\033[0m  independent terminal apps\n\n'
printf 'Real Go UIs. Local file preview.\n'
printf 'GitHub + VM provider data below are labeled fixtures.\n\n'
