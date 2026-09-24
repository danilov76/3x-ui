import importlib.util
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch, Mock

spec = importlib.util.spec_from_file_location('installer', Path(__file__).with_name('install-fork.py'))
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class InstallerTest(unittest.TestCase):
    def test_archive_escape_and_links_rejected(self):
        with tempfile.TemporaryDirectory() as folder:
            target = Path(folder)
            for name, kind in [('../escape', tarfile.REGTYPE), ('/absolute', tarfile.REGTYPE), ('link', tarfile.SYMTYPE)]:
                archive = target/'bad.tar.gz'
                with tarfile.open(archive, 'w:gz') as t:
                    entry = tarfile.TarInfo(name)
                    entry.type = kind
                    entry.linkname = '/etc/passwd'
                    t.addfile(entry)
                with self.assertRaisesRegex(ValueError, 'Unsafe archive'):
                    installer.unpack(archive, target/'out')
                self.assertFalse((target/'out').exists())

    def test_existing_installation_refused_before_commands(self):
        with patch.object(installer.os, 'geteuid', return_value=0), \
             patch.object(installer.os, 'uname', return_value=Mock(machine='x86_64')), \
             patch.object(Path, 'read_text', return_value='ID=ubuntu\nVERSION_ID="24.04"'), \
             patch.object(Path, 'is_dir', return_value=True), \
             patch.object(installer.os.path, 'lexists', side_effect=lambda p: p == '/etc/x-ui'), \
             patch.object(installer.subprocess, 'run') as run:
            with self.assertRaisesRegex(ValueError, 'Existing installation'):
                installer.preflight()
            run.assert_not_called()

    def test_hostname_rejects_certificate_injection(self):
        for value in ['example.org/CN=other', 'foo\nDNS:bar', 'https://example.org', 'host;id']:
            with self.assertRaises(Exception):
                installer.host_value(value)
        for value in ['example.org', '192.0.2.1', '2001:db8::1']:
            self.assertEqual(installer.host_value(value), value)


if __name__ == '__main__':
    unittest.main()
