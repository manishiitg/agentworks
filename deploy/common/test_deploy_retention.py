"""Keep all directory-based Linux deployment entry points on shared cleanup."""
from pathlib import Path
import subprocess
import unittest

DEPLOY = Path(__file__).resolve().parents[1]


class DeploymentRetentionTest(unittest.TestCase):
    def test_release_installers_require_agent_and_workspace_health(self):
        installers = {
            'aws-ec2/server/build-and-activate.sh': (8000, 8080),
            'aws-ec2/rootless/migrate-once.sh': (8000, 8080),
        }
        for name, (agent, workspace) in installers.items():
            with self.subTest(script=name):
                script = (DEPLOY / name).read_text()
                self.assertIn('prune-releases.py', script)
                self.assertIn('--apply', script)
                self.assertIn(f'--health-url http://127.0.0.1:{agent}/api/health', script)
                self.assertIn(f'--health-url http://127.0.0.1:{workspace}/health', script)

    def test_every_release_builder_packages_the_shared_helper(self):
        for name in ('aws-ec2/server/build-and-activate.sh', 'rootless-linux/build-and-activate.sh'):
            with self.subTest(script=name):
                self.assertIn('/deploy/common/prune-releases.py', (DEPLOY / name).read_text())

    def test_shared_rootless_builder_checks_both_product_health_endpoints(self):
        script = (DEPLOY / 'rootless-linux/build-and-activate.sh').read_text()
        self.assertIn('--health-url "http://127.0.0.1:$AGENT_PORT/api/health"', script)
        self.assertIn('--health-url "http://127.0.0.1:$WORKSPACE_PORT/health"', script)

    def test_repository_root_is_the_only_local_deploy_entry_point(self):
        for removed in ('aws-ec2/deploy-rootless.sh', 'aws-ec2/deploy-aws-ec2.sh', 'rootless-linux/deploy.sh',
                        'cf/deploy-cf.sh', 'azure', 'dedicated-vm/deploy.sh'):
            with self.subTest(removed=removed):
                self.assertFalse((DEPLOY / removed).exists())
        entry = (DEPLOY.parent / 'deploy.sh').read_text()
        for function in ('deploy_rts()', 'report_rts_cloudfront_usage()', 'deploy_rootless_product() ('):
            self.assertIn(function, entry)
        subprocess.run(['bash', '-n', str(DEPLOY.parent / 'deploy.sh')], check=True, capture_output=True)

    def test_linux_shell_scripts_parse(self):
        for path in DEPLOY.rglob('*.sh'):
            with self.subTest(script=str(path.relative_to(DEPLOY))):
                subprocess.run(['bash', '-n', str(path)], check=True, capture_output=True)


if __name__ == '__main__':
    unittest.main()
