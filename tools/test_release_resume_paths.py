"""Regress the actual release adapters without network or user credentials."""
import copy
import json
import os
import subprocess
import sys
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import release
import release_ios
import test_release as fixtures
from test_release_windows import write_windows_zip


class RetainedWindowsTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(); self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name); (self.root / 'bin').mkdir()
        self.files = release.windows_release_files(self.root, '1.2.3')
        self.assets = {}
        for path, arch, machine in zip(self.files, ('amd64', 'arm64'), (0x8664, 0xaa64)):
            write_windows_zip(path, machine)
            self.assets[arch] = dict(status='passed', arch=arch, asset_name=path.name,
                                     asset_sha256=release.sha256(path))
        self.accepted = dict(status='passed', version='1.2.3', source_sha='sha', assets=self.assets)
        self.ledger = self.root / 'delivery.json'; self.remote = []; self.calls = []
        self.env = patch.dict(os.environ, {'DELIVERY_STATE': str(self.ledger)})
        self.env.start(); self.addCleanup(self.env.stop)
        self.persist()

    def persist(self):
        release.write_json(self.ledger, {'pending_new_batch': dict(version='1.2.3',
            source_sha='sha', windows='ready_for_publication', issues=[],
            release_windows_validation=self.accepted)})

    def command(self, args, **kwargs):
        self.calls.append(args)
        if args[:2] == ['gh', 'api']:
            return json.dumps([[dict(tag_name='v1.2.3', assets=self.remote)]])
        if args[0] == 'make': raise AssertionError('Accepted bytes must never be rebuilt')
        return ''

    def publish(self):
        with patch.object(release, 'run', side_effect=self.command), patch.object(
                release, 'source_preflight', return_value='sha'):
            return release.github_platform(self.root, '1.2.3', 'sha', 'windows', {})

    def test_first_upload_reuses_both_accepted_archives(self):
        before = [p.read_bytes() for p in self.files]
        self.assertEqual(self.publish()['status'], 'published')
        self.assertEqual(before, [p.read_bytes() for p in self.files])
        self.assertEqual(len([c for c in self.calls if c[0] == 'go']), 1)

    def test_invalid_receipts_or_files_refuse_before_make_or_upload(self):
        original = copy.deepcopy(self.accepted)
        for mutate in [lambda r: r.update(source_sha='other'),
                       lambda r: r.update(status='pending'),
                       lambda r: r.update(version='1.2.4'),
                       lambda r: r['assets'].pop('arm64'),
                       lambda r: r['assets']['amd64'].update(arch='arm64'),
                       lambda r: r['assets']['arm64'].update(asset_sha256='0'*64)]:
            with self.subTest(mutate=mutate):
                self.accepted = copy.deepcopy(original); mutate(self.accepted); self.persist()
                self.calls.clear()
                with self.assertRaises(ValueError): self.publish()
                self.assertFalse(any(c[0] in ('go', 'make') for c in self.calls))
        self.accepted = original; self.persist(); self.files[1].unlink()
        with self.assertRaises(ValueError): self.publish()

    def test_partial_publication_checks_public_bytes_and_never_rebuilds_missing_arch(self):
        self.remote = [dict(name=self.files[0].name, digest='sha256:'+release.sha256(self.files[0]))]
        self.assertEqual(self.publish()['status'], 'published')
        self.files[0].write_bytes(b'changed public archive'); self.calls.clear()
        with self.assertRaises(ValueError): self.publish()
        self.assertFalse(any(c[0] in ('go', 'make') for c in self.calls))

    def test_matching_receipt_cannot_accept_wrong_pe_architecture(self):
        write_windows_zip(self.files[0], 0xaa64)
        self.assets['amd64']['asset_sha256'] = release.sha256(self.files[0]); self.persist()
        with self.assertRaisesRegex(ValueError, 'amd64'): self.publish()
        self.assertFalse(any(c[0] == 'go' for c in self.calls))

    def test_public_pair_cannot_hide_conflicting_native_receipt(self):
        self.remote = [dict(name=p.name,digest='sha256:'+release.sha256(p)) for p in self.files]
        self.assets['amd64']['asset_sha256']='0'*64; self.persist()
        with self.assertRaises(ValueError):self.publish()
        self.assertFalse(any(c[0] in ('go','make') for c in self.calls))

    def test_partial_public_pair_without_new_arch_receipt_refuses_rebuild(self):
        self.remote = [dict(name=self.files[0].name,digest='sha256:'+release.sha256(self.files[0]))]
        data=release.read_json(self.ledger);data['pending_new_batch'].pop('release_windows_validation')
        release.write_json(self.ledger,data)
        with self.assertRaises(ValueError):self.publish()
        self.assertFalse(any(c[0] in ('go','make') for c in self.calls))


class PublicationOrderTests(unittest.TestCase):
    def test_ios_wait_starts_only_after_all_other_platform_results_are_durable(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); state = root/'state.json'; ledger = root/'delivery.json'
            platforms = ['macos', 'android', 'ios', 'windows', 'linux']; order = []
            release.write_json(ledger, {'pending_new_batch': dict(version='1.2.3',
                source_sha='sha', issues=[1]), 'issues': {'1':dict.fromkeys(platforms, 'ready_for_publication')}})
            def published(*args, **kwargs):
                order.append(args[3]); return {'status':'published'}
            def ios_publish(**kwargs):
                saved = release.read_json(state)
                self.assertEqual(order, ['macos','android','windows','linux'])
                self.assertTrue(all(saved[p]['status']=='published' for p in order))
                order.append('ios'); return {'status':'waiting_processing'}
            with patch.dict(os.environ, {'DELIVERY_STATE':str(ledger)}), patch.object(
                    release, 'source_preflight', return_value='sha'), patch.object(
                    release, 'github_platform', side_effect=published), patch.object(
                    release, 'release_notes'), patch.object(release_ios, 'IOSRelease') as ios:
                ios.return_value.publish.side_effect=ios_publish
                self.assertEqual(release.execute(root,'1.2.3','all',state),1)
            self.assertEqual(order, ['macos','android','windows','linux','ios'])
            self.assertEqual(release.read_json(state)['ios']['status'],'waiting_processing')


class IOSHistoryTests(unittest.TestCase):
    setUp = fixtures.IosTests.setUp
    save_config = fixtures.IosTests.save_config
    build = fixtures.IosTests.build
    ipa = fixtures.IosTests.ipa
    receipt = fixtures.IosTests.receipt

    def ios(self): return release_ios.IOSRelease(self.root,'0.1.20','sha',self.state)

    def test_completed_history_needs_no_detail_or_review_requests(self):
        self.receipt(); self.apple.builds.extend(self.build(str(n)) for n in range(1,120))
        release.write_json(self.root/'ios-signing/testflight-releases.json', {'releases':[
            dict(app_id='app',build_number=str(n),successfully_published=True,
                 success_beijing_date='2000-01-01') for n in range(1,120)]})
        original=self.apple.request
        def current_only(method,path,body=None):
            if path.startswith('builds/') and not path.startswith('builds/build120/'):
                raise AssertionError('Unexpected completed-history query: '+path)
            return original(method,path,body)
        with patch.object(self.apple,'request',side_effect=current_only):
            self.assertEqual(self.ios().publish_once()['status'],'waiting_beta_review')

    def test_unknown_candidate_still_blocks_availability_mutations(self):
        self.receipt(); self.apple.builds.append(self.build('119'))
        with self.assertRaisesRegex(ValueError,'pending'): self.ios().publish_once()
        self.assertFalse(self.actions)

    def test_current_status_is_strict_and_does_not_record_delivery_or_call_quota(self):
        self.receipt(); self.apple.ready=True
        self.apple.builds[0]['attributes']['expired']=False
        for group in self.apple.members: self.apple.members[group]=self.apple.builds
        ios=self.ios()
        with patch.object(ios,'quota',side_effect=AssertionError('Read-only status cannot scan history')):
            result=ios.current_status()
        self.assertEqual(result['status'],'available')
        self.assertTrue(result['groups_forward']==result['groups_reverse']==['external','internal'])
        self.assertFalse(self.actions); self.assertFalse(ios.ledger_path.exists())
        self.assertNotIn('first_publication',self.state)
        self.apple.members['external']=[]
        self.assertEqual(self.ios().current_status()['status'],'waiting_beta_review')

    def test_status_rejects_reverse_missing_unknown_expiry_and_wrong_marketing(self):
        self.receipt(); self.apple.ready=True
        for group in self.apple.members: self.apple.members[group]=self.apple.builds
        attrs=self.apple.builds[0]['attributes']; attrs['expired']=False
        original=self.apple.all
        def missing_reverse(path):
            return [] if path.startswith('betaGroups?filter') else original(path)
        with patch.object(self.apple,'all',side_effect=missing_reverse):
            self.assertEqual(self.ios().current_status()['status'],'waiting_beta_review')
        for key,value,status in [('expired',True,'expired'),('expired',None,'waiting_beta_review'),
                                 ('processingState','FAILED','processing_failed'),
                                 ('processingState','PROCESSING','waiting_processing')]:
            attrs.update(expired=False,processingState='VALID'); attrs[key]=value
            self.assertEqual(self.ios().current_status()['status'],status)
        attrs.update(expired=False,processingState='VALID')
        original_request=self.apple.request
        def wrong_marketing(method,path,body=None):
            if path.endswith('/preReleaseVersion'):return {'data':{'attributes':{'version':'9.9.9'}}}
            return original_request(method,path,body)
        with patch.object(self.apple,'request',side_effect=wrong_marketing):
            with self.assertRaisesRegex(ValueError,'marketing'):self.ios().current_status()
        self.assertFalse(self.actions)

    def test_cli_status_uses_get_only_and_leaves_all_local_files_unchanged(self):
        source=Path(__file__).resolve().parents[1]
        sha=subprocess.check_output(['git','rev-parse','HEAD'],cwd=source,text=True).strip()
        client=self.root/'readonly_client.py'
        client.write_text("""APP_ID='app'
class Client:
    def all(self,path):
        if '/buildUploads' in path:return []
        if '/betaGroups' in path or path.startswith('betaGroups?'):
            return [{'id':'internal','attributes':{'isInternalGroup':True}},
                    {'id':'external','attributes':{'isInternalGroup':False}}]
        return [{'id':'b120','attributes':{'version':'120','processingState':'VALID','expired':False}}]
    def request(self,method,path,body=None):
        assert method=='GET','Mutation attempted'
        if path.endswith('/preReleaseVersion'):return {'data':{'attributes':{'version':'0.1.20'}}}
        if path.endswith('/buildBetaDetail'):
            return {'data':{'attributes':{'internalBuildState':'READY_FOR_BETA_TESTING','externalBuildState':'IN_BETA_TESTING'}}}
        if path.endswith('/betaAppReviewSubmission'):return {'data':{'attributes':{'betaReviewState':'APPROVED'}}}
        raise AssertionError(path)
""")
        self.config.update(testflight_cli=str(client),marketing_version='0.1.20');self.save_config()
        state=self.root/'releases/0.1.20.json'
        release.write_json(state,dict(version='0.1.20',source_sha=sha,ios={'source_sha':sha}))
        before={str(p):p.read_bytes() for p in self.root.rglob('*') if p.is_file()}
        result=subprocess.run([sys.executable,'-B',str(source/'tools/release.py'),'--version','0.1.20',
            '--platform','ios','--ios-status','--source-dir',str(source)],capture_output=True,text=True,timeout=15)
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual(json.loads(result.stdout)['status'],'available')
        for incompatible in (['--check'],['--verify'],['--platform','windows'],['--urgent-reason','authorized']):
            refused=subprocess.run([sys.executable,'-B',str(source/'tools/release.py'),'--version','0.1.20',
                '--platform','ios','--ios-status',*incompatible],capture_output=True,text=True,timeout=15)
            self.assertEqual(refused.returncode,2,refused.stderr)
        self.assertEqual(before,{str(p):p.read_bytes() for p in self.root.rglob('*') if p.is_file()})
