#!/bin/sh
# Register a deliberately unopened sample. No cloud or Rex API calls are made.
set -eu

if [ "$#" -lt 1 ] || [ "$#" -gt 3 ]; then
    echo "Usage: $0 /absolute/project/root [OWNER/REPOSITORY [EXISTING_VM_NAME]]" >&2
    exit 2
fi

demo_root=$1
case "$demo_root" in
    /*) ;;
    *) echo "Use an explicit absolute project root." >&2; exit 2 ;;
esac

demo_script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
demo_cli="$demo_script_dir/../bin/rawr"

if [ "$#" -eq 3 ]; then
    "$demo_cli" create rawr-demo "$demo_root" --tools files,github,exedev --repository "$2" --vm "$3"
elif [ "$#" -eq 2 ]; then
    "$demo_cli" create rawr-demo "$demo_root" --tools files,github,exedev --repository "$2"
else
    "$demo_cli" create rawr-demo "$demo_root" --tools files,github,exedev
fi

"$demo_cli" list
echo 'Registered only. Copy the project ID above, then run: rawr open PROJECT_ID'
