import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
from .state import Error, Store
from .rex import Rex
from .project import Projects
from .services import ServiceError
from .integration import IntegrationError


def parser():
    p=argparse.ArgumentParser(prog='rawr',description='Deliberately saved projects in independent native Rex panes.')
    p.add_argument('--config-dir'); p.add_argument('--state-dir'); p.add_argument('--rex'); p.add_argument('--server'); p.add_argument('--client')
    sub=p.add_subparsers(dest='command',required=True)
    c=sub.add_parser('create'); c.add_argument('name'); c.add_argument('root'); c.add_argument('--tools',default=''); c.add_argument('--repository'); c.add_argument('--vm')
    sub.add_parser('list')
    for cmd in ('open','status','plan','restore','show'):
        c=sub.add_parser(cmd); c.add_argument('project')
    c=sub.add_parser('save'); c.add_argument('project'); c.add_argument('--expected-revision',type=int)
    c=sub.add_parser('declare'); c.add_argument('project'); c.add_argument('role'); c.add_argument('--kind',choices=['shell','command','files','github','exedev'],default='command'); c.add_argument('argv',nargs=argparse.REMAINDER)
    c=sub.add_parser('open-tool'); c.add_argument('project'); c.add_argument('role')
    c=sub.add_parser('toggle'); c.add_argument('project'); c.add_argument('role',choices=['files','github','exedev'])
    c=sub.add_parser('toggle-session'); c.add_argument('role',choices=['files','github','exedev']); c.add_argument('--session',required=True)
    c=sub.add_parser('associate'); c.add_argument('project'); c.add_argument('--session',required=True)
    c=sub.add_parser('link'); c.add_argument('project'); c.add_argument('vm')
    c=sub.add_parser('vm-shell'); c.add_argument('project')
    c=sub.add_parser('vm-main'); c.add_argument('project')
    c=sub.add_parser('tool'); c.add_argument('kind',choices=['files','github','exedev']); c.add_argument('project')
    sub.add_parser('pick')
    c=sub.add_parser('install'); c.add_argument('--config')
    c=sub.add_parser('uninstall'); c.add_argument('--config')
    sub.add_parser('doctor')
    return p


def output(value):
    print(json.dumps(value,indent=2,ensure_ascii=False))


def picker(projects):
    if not sys.stdin.isatty() or not sys.stdout.isatty():
        raise Error('Project picker needs a terminal; use the Rex Projects action or run it in a terminal.')
    import curses
    from .tools import safe_text
    def ui(screen):
        cursor=0; message=''
        while True:
            items=projects.store.projects(); screen.erase(); height,width=screen.getmaxyx()
            lines=['Rawr · projects','↑/↓ choose · Enter open · q close',message]
            for index,item in enumerate(items):
                lines.append(('› ' if index==cursor else '  ')+item['name']+'  '+item['id'][:8]+'  '+item['root'])
            if not items:
                lines.append('No projects. In a shell: rawr create NAME /absolute/root --tools files,github,exedev')
            for row,line in enumerate(lines[:max(0,height-1)]):
                try: screen.addnstr(row,0,safe_text(line),max(0,width-1))
                except curses.error: pass
            screen.refresh(); key=screen.getch()
            if key in (ord('q'),27): return
            if key in (curses.KEY_UP,ord('k')): cursor=max(0,cursor-1)
            elif key in (curses.KEY_DOWN,ord('j')): cursor=min(max(0,len(items)-1),cursor+1)
            elif items and key in (10,13):
                item=items[min(cursor,len(items)-1)]
                try:
                    projects.open(item['id']); return
                except Error as exc: message=str(exc)
    curses.wrapper(ui)


def main(argv=None):
    args=parser().parse_args(argv)
    store=Store(args.config_dir,args.state_dir)
    rex=Rex(args.rex,args.server,args.client)
    projects=Projects(store,rex)
    try:
        cmd=args.command
        if cmd=='create': result=projects.create(args.name,args.root,filter(None,args.tools.split(',')),args.repository,args.vm)
        elif cmd=='list': result=[{'id':p['id'],'name':p['name'],'root':p['root'],'revision':p['revision']} for p in store.projects()]
        elif cmd=='show': result=store.resolve(args.project)
        elif cmd=='open': result=projects.open(args.project)
        elif cmd=='open-tool': result=projects.tool(args.project,args.role)
        elif cmd=='toggle': result=projects.toggle(args.project,args.role)
        elif cmd=='toggle-session': result=projects.toggle(projects.project_for_session(args.session),args.role)
        elif cmd=='save': result=projects.save(args.project,args.expected_revision)
        elif cmd in ('status','plan'): result=projects.plan(args.project)
        elif cmd=='restore': result=projects.restore(args.project)
        elif cmd=='associate': result=projects.associate(args.project,args.session)
        elif cmd=='declare':
            command=args.argv[1:] if args.argv[:1]==['--'] else args.argv
            result=projects.add_tool(args.project,args.kind,command,args.role)
        elif cmd=='link':
            from .services import exe_find
            vm=exe_find(args.vm); result=projects.link_vm(args.project,vm['vm_name'])
        elif cmd=='vm-shell':
            from .services import exe_find
            p=store.resolve(args.project)
            if not p.get('vm'): raise Error('No linked VM. Use `link PROJECT VM` or the exe.dev picker.')
            result=projects.vm_shell(p['id'],exe_find(p['vm']))
        elif cmd=='vm-main':
            from .vm_main import migrate_main_slot
            result=migrate_main_slot(projects,args.project)
        elif cmd=='tool':
            from .tools import run_tool
            p=store.resolve(args.project)
            run_tool(args.kind,p,{'link_vm':lambda name:projects.link_vm(p['id'],name),
                'vm_shell':lambda vm:projects.vm_shell(p['id'],vm),'switch_vm':lambda vm:projects.switch_vm(p['id'],vm),
                'reload_project':lambda:store.resolve(p['id'])})
            return 0
        elif cmd=='pick': picker(projects); return 0
        elif cmd in ('install','uninstall'):
            from .integration import install,uninstall
            # Include explicit state/server context for isolated installations.
            launcher=[sys.executable,str(Path(__file__).resolve().parents[1]/'bin/rawr')]
            for key,value in (('--config-dir',str(store.config) if args.config_dir else None),('--state-dir',str(store.runtime) if args.state_dir else None),('--rex',rex.executable),('--server',args.server),('--client',args.client)):
                if value: launcher += [key,value]
            def check(file):
                try:
                    r=subprocess.run([rex.executable,'config','check',str(file),'--json'],capture_output=True,text=True,timeout=10)
                except (OSError,subprocess.TimeoutExpired) as exc:
                    raise Error(f'Cannot validate Rex config: {exc}') from exc
                if r.returncode: raise Error('Rex rejected candidate config: '+r.stderr[:1200])
            result=install(args.config,launcher,check) if cmd=='install' else uninstall(args.config,check)
        elif cmd=='doctor':
            identity=rex.identity()
            result={'rex_version':identity['rex_version'],'host_id':identity['host_id'],'instance_id':identity['instance_id'],
                    'config_dir':str(store.config),'state_dir':str(store.runtime),'local_root':True,
                    'dependencies':{p:bool(__import__('shutil').which(p)) for p in ('gh','ssh','open')},
                    'limits':['Rex restores fresh shells; programs/conversations are not restored','Tiled setup snapshots only',
                              'Fixed pane sizes require Rex Interface > Pane Behavior > Autobalance splits OFF',
                              'Remote Rex servers unsupported','exe.dev Claude/imp credential lending is not implemented']}
        else: raise Error('Unknown command')
        output(result)
        return 2 if isinstance(result,dict) and result.get('result') in ('partial','failed') else 0
    except (Error,ServiceError,IntegrationError,OSError,ValueError) as exc:
        print(f'rawr: {exc}',file=sys.stderr); return 1
    except KeyboardInterrupt:
        return 130


if __name__=='__main__':
    raise SystemExit(main())
