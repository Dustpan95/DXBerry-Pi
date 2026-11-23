"""Compiled CLI checks in hardware-free, network-disabled Docker, off station."""
import argparse,glob,hashlib,json,os,pathlib,socket,subprocess,tempfile,time

if not __debug__:raise RuntimeError('test assertions must be enabled')
assert pathlib.Path('/.dockerenv').exists() and os.geteuid()==0, 'requires a Docker root test driver'
assert all(name=='lo' for _,name in socket.if_nameindex()), 'requires --network none'
assert not any(glob.glob(p) for p in ('/dev/snd','/dev/ttyUSB*','/dev/ttyACM*','/dev/gpiochip*','/dev/bus/usb')), 'hardware must not be mounted'
parser=argparse.ArgumentParser()
parser.add_argument('--binary',default='/fixture/dxberry-preset')
parser.add_argument('--output',default='/evidence/cli-integration.json')
args=parser.parse_args()
binary=args.binary
report_path=pathlib.Path(args.output)
assert not report_path.exists() and not report_path.is_symlink(), 'refusing existing evidence destination'
checks=[]
with tempfile.TemporaryDirectory(prefix='preset-cli-') as state:
 os.chmod(state,0o700)
 marker=pathlib.Path(state,'unrelated');marker.write_text('retain fixture configuration\n')
 marker_hash=hashlib.sha256(marker.read_bytes()).hexdigest()
 def call(req,expected=0,uid=None):
  def drop():
   os.setgroups([]);os.setgid(uid);os.setuid(uid)
  p=subprocess.run([binary,'--json','--state',state],input=json.dumps(req),text=True,capture_output=True,timeout=8,preexec_fn=drop if uid is not None else None)
  assert p.returncode==expected,(p.returncode,p.stdout,p.stderr)
  assert not p.stderr,p.stderr
  return json.loads(p.stdout)
 listreq={'version':1,'operation':'list','radio':'radio1'}
 r=call(listreq);assert r['ok'] and r['library']['revision']=='absent'
 assert not pathlib.Path(state,'presets').exists()
 checks.append('actual binary lists absent state without creating files')
 create={'version':1,'operation':'create','radio':'radio1','expected_revision':'absent','label':'receive fixture','frequency_hz':14074000}
 r=call(create);lib=r['library'];assert r['ok'] and len(lib['presets'])==1
 assert pathlib.Path(state,'presets').stat().st_mode&0o777==0o700
 assert pathlib.Path(state,'presets/radio1.json').stat().st_mode&0o777==0o600
 checks.append('actual root binary persists private definitions with generated identities')
 r=call(create,4);assert r['code']=='conflict' and 'library' not in r
 checks.append('actual CLI refuses stale creation without returning prior state as success')
 req={'version':1,'operation':'update','radio':'radio1','expected_revision':lib['revision'],'id':lib['presets'][0]['id'],'label':'renamed fixture','frequency_hz':7100000}
 r=call(req);lib=r['library'];assert lib['presets'][0]['frequency_hz']==7100000 and lib['presets'][0]['label']=='renamed fixture'
 r=call({'version':1,'operation':'delete','radio':'radio1','expected_revision':lib['revision'],'id':lib['presets'][0]['id']})
 assert r['library']['presets']==[] and r['library']['revision']!='absent'
 checks.append('actual CLI updates and deletes while retaining an empty revisioned library')
 r=call(listreq,6,65534);assert r['code']=='unavailable' and 'requires root' in r['reason'] and 'library' not in r
 checks.append('actual nonroot process is refused before state access')
 for bad in [dict(listreq,operation='recall'),dict(listreq,command='set_ptt 1'),dict(listreq,state='/other'),dict(listreq,radio='../outside')]:
  r=call(bad,2);assert r['code']=='invalid' and 'library' not in r
 checks.append('actual binary rejects control/path fields and unsupported operations')
 p=subprocess.Popen([binary,'--json','--state',state],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
 start=time.monotonic()
 try:
  code=p.wait(timeout=8);out=p.stdout.read();err=p.stderr.read()
  assert code==6 and time.monotonic()-start<8 and not err
  assert json.loads(out)['code']=='unavailable'
 finally:
  p.stdin.close()
  if p.poll() is None:p.kill();p.wait()
 checks.append('actual OS stdin pipe is closed after the input deadline')
 assert hashlib.sha256(marker.read_bytes()).hexdigest()==marker_hash
 assert sorted(x.name for x in pathlib.Path(state).iterdir())==['presets','unrelated']
 checks.append('unrelated fixture configuration remains unchanged')

report={'version':1,'kind':'preset-cli-software-integration','result':'passed','binary_sha256':hashlib.sha256(pathlib.Path(binary).read_bytes()).hexdigest(),'checks':checks,'radio_control_tested':False,'physical_acceptance':False,'pi_installed':False}
with report_path.open('x') as f:f.write(json.dumps(report,indent=2)+'\n')
print(json.dumps(report))
