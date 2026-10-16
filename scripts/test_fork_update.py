import importlib.util
import io
import tarfile
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location('updater', Path(__file__).resolve().parents[1] / 'internal/web/service/panel/fork-update.py')
u = importlib.util.module_from_spec(spec)
spec.loader.exec_module(u)

class UpdateTest(unittest.TestCase):
    def release(self):
        return {'tag_name': 'tunnels-3.8.5-patch5', 'assets': [{'name': u.ASSET,
                'browser_download_url': f'{u.REPO}/releases/download/tunnels-3.8.5-patch5/{u.ASSET}',
                'digest': 'sha256:' + 'a' * 64}]}

    def test_compatible_release(self):
        self.assertEqual(u.release_asset(self.release())[1], 'a' * 64)

    def test_reject_wrong_source_or_no_digest(self):
        for field, value in [('browser_download_url', 'https://github.com/MHSanaei/3x-ui/evil'), ('digest', '')]:
            r = self.release()
            r['assets'][0][field] = value
            with self.assertRaises(ValueError): u.release_asset(r)

    def test_reject_upstream_dev_and_pre_bootstrap(self):
        for tag in ['v3.9.0', 'dev-latest', 'tunnels-3.8.5-patch4']:
            r = self.release(); r['tag_name'] = tag
            with self.assertRaises(ValueError): u.release_asset(r)

    def test_reject_unsafe_archive(self):
        for name, typ in [('../outside', tarfile.REGTYPE), (u.ROOT+'/link', tarfile.SYMTYPE), ('/absolute', tarfile.REGTYPE)]:
            with tempfile.TemporaryDirectory() as d:
                p = Path(d)/'a.tar'
                with tarfile.open(p, 'w') as t:
                    m = tarfile.TarInfo(name); m.type = typ; t.addfile(m)
                with self.assertRaises(ValueError): u.unpack(p, Path(d)/'out')

if __name__ == '__main__': unittest.main()
