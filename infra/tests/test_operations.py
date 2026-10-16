import importlib.util
import io
import json
import os
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
def load(name):
    spec = importlib.util.spec_from_file_location(name, ROOT / (name + '.py'))
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module); return module
host = load('host')
dispatch = load('dispatch')

class ArchiveTests(unittest.TestCase):
    def archive(self, extras):
        out = io.BytesIO()
        with tarfile.open(fileobj=out, mode='w') as t:
            for name, kind in [('etc/x-ui/x-ui.db', tarfile.REGTYPE), ('usr/local/x-ui/x-ui', tarfile.REGTYPE), (host.STATE_MEMBER, tarfile.REGTYPE)] + extras:
                m = tarfile.TarInfo(name); m.type = kind
                if kind == tarfile.SYMTYPE: m.linkname = '/etc/shadow'
                t.addfile(m, io.BytesIO())
        out.seek(0)
        return tarfile.open(fileobj=out)
    def test_snapshot_accepted(self):
        with self.archive([('etc/systemd/system/xui-tunnel-vk-0123456789abcdef.service', tarfile.REGTYPE)]) as t:
            host.validate_archive(t)
    def test_rejects_traversal_links_duplicates_and_system_files(self):
        for name, kind in [('etc/x-ui/../../shadow', tarfile.REGTYPE), ('/etc/x-ui/x-ui.db', tarfile.REGTYPE),
                ('etc/x-ui/link', tarfile.SYMTYPE), ('etc/shadow', tarfile.REGTYPE),
                ('etc/systemd/system/ssh.service', tarfile.REGTYPE), ('etc/x-ui/x-ui.db', tarfile.REGTYPE)]:
            with self.subTest(name=name), self.archive([(name, kind)]) as t:
                with self.assertRaises(ValueError): host.validate_archive(t)
    def test_no_master_restore_without_confirmation(self):
        with patch.object(host, 'run') as run:
            with self.assertRaises(ValueError): host.restore({})
            run.assert_not_called()
    def test_existing_db_is_never_overwritten(self):
        with tempfile.TemporaryDirectory() as d:
            db = Path(d) / 'existing';db.write_text('keep')
            with patch.object(host, 'DB', db):
                with self.assertRaises(ValueError): host.restore({'old_master_stopped':True})
            self.assertEqual(db.read_text(), 'keep')
    def test_backup_requires_encryption_recipient(self):
        with self.assertRaises(ValueError):host.backup({'age_recipient':'not-a-recipient'})

class DispatchTests(unittest.TestCase):
    def test_ssh_injection_refused(self):
        for h in ['-oProxyCommand=bad','example.org;bad','x\nHost *','user@host']:
            with self.assertRaises(ValueError):dispatch.validate({'host':h})
    def test_no_secrets_in_failed_dispatch_output(self):
        env={'OPERATION':'check','TARGET_CONFIG':json.dumps({'host':'example.org'}),
             'SSH_PRIVATE_KEY':'PRIVATE_SECRET','SSH_KNOWN_HOSTS':'HOST_SECRET'}
        import contextlib
        out=io.StringIO()
        with patch.dict(os.environ,env), patch.object(dispatch.subprocess,'run') as r, contextlib.redirect_stdout(out):
            r.return_value.returncode=1
            with self.assertRaises(ValueError):dispatch.main()
        self.assertNotIn('SECRET',out.getvalue())
    def test_inventory_strict_host_checking(self):
        env={'OPERATION':'check','TARGET_CONFIG':json.dumps({'host':'example.org'}),'SSH_PRIVATE_KEY':'private','SSH_KNOWN_HOSTS':'known'}
        def fake_run(argv, **kw):
            inv=json.loads(Path(argv[2]).read_text())
            self.assertIn('StrictHostKeyChecking=yes',inv['all']['hosts']['target']['ansible_ssh_common_args'])
            self.assertEqual(Path(inv['all']['hosts']['target']['ansible_ssh_private_key_file']).stat().st_mode & 0o777,0o600)
            from types import SimpleNamespace
            return SimpleNamespace(returncode=0)
        with patch.dict(os.environ,env),patch.object(dispatch.subprocess,'run',fake_run):dispatch.main()

if __name__ == '__main__':unittest.main()

class RestoreTests(unittest.TestCase):
    def make_archive(self, d):
        import sqlite3
        db=Path(d)/'db'
        with sqlite3.connect(db) as c:c.execute('create table marker(value text)');c.execute("insert into marker values('preserve')")
        f=Path(d)/'encrypted-fixture'
        with tarfile.open(f,'w:gz') as t:
            t.add(db,arcname='etc/x-ui/x-ui.db')
            for name,data in [('usr/local/x-ui/x-ui',b'binary'),('etc/systemd/system/x-ui.service',b'unit'),
                     (host.STATE_MEMBER,json.dumps({'active':['x-ui.service'],'enabled':['x-ui.service']}).encode())]:
                m=tarfile.TarInfo(name);m.size=len(data);m.mode=0o600;t.addfile(m,io.BytesIO(data))
        return f
    def exercise(self, fail_health=False, conflict=False):
        import shutil
        from types import SimpleNamespace
        with tempfile.TemporaryDirectory() as d:
            root=Path(d)/'target';root.mkdir();archive=self.make_archive(d)
            if conflict:
                p=root/'etc/systemd/system/x-ui.service';p.parent.mkdir(parents=True);p.write_text('existing')
            calls=[]
            def fake(*args,**kw):
                calls.append(args)
                if args[0]=='age':shutil.copyfile(args[-1],args[-2])
                return SimpleNamespace(stdout=b'',returncode=0)
            config={'old_master_stopped':True,'restore_path':str(archive),'restore_sha256':host.digest(archive),'age_identity':'fixture'}
            with patch.object(host,'RESTORE_ROOT',root),patch.object(host,'DB',root/'etc/x-ui/x-ui.db'),patch.object(host,'PANEL',root/'usr/local/x-ui/x-ui'),patch.object(host,'run',fake),patch.object(host,'units',return_value=['x-ui.service']),patch.object(host,'health',side_effect=ValueError() if fail_health else None),patch.object(host.subprocess,'run') as cleanup:
                if fail_health or conflict:
                    with self.assertRaises(ValueError):host.restore(config)
                else:host.restore(config)
                if fail_health:cleanup.assert_called_with(['systemctl','disable','--now','x-ui.service'],capture_output=True)
                if conflict:
                    self.assertFalse((root/'etc/x-ui/x-ui.db').exists())
                    self.assertEqual(p.read_text(),'existing')
                else:self.assertTrue((root/'etc/x-ui/x-ui.db').exists())
    def test_clean_restore(self):self.exercise()
    def test_failed_health_stops_restored_services(self):self.exercise(fail_health=True)
    def test_conflict_checked_before_any_write(self):self.exercise(conflict=True)
