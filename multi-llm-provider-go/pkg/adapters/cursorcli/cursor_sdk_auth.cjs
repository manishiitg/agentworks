// Fixed account actions. Never print SDK login results: they contain the credential.
const { Cursor } = require(process.argv[1]);
const action = process.argv[2];
(async () => {
  if (action === 'login') {
    await Cursor.auth.login({openBrowser: false, onLoginUrl: url => {
      process.stdout.write('Open this link to sign in with Cursor:\n' + url + '\n');
    }});
    process.stdout.write('Signed in with Cursor. You can close this window.\n');
  } else if (action === 'logout') {
    await Cursor.auth.logout();
    process.stdout.write('Signed out of Cursor.\n');
  } else if (action === 'models') {
    const models = await Cursor.models.list({apiKey: process.env.CURSOR_API_KEY || undefined});
    for (const model of models) {
      process.stdout.write(`${model.id} - ${model.displayName}\n`);
      for (const variant of model.variants || []) {
        if (!variant.params?.length) continue;
        const params = variant.params.map(p => `${p.id}=${p.value}`).join(',');
        process.stdout.write(`${model.id}[${params}] - ${variant.displayName}\n`);
      }
    }
  } else if (action === 'status') {
    let loggedIn = false, email;
    if (process.env.CURSOR_API_KEY) {
      try {
        const user = await Cursor.me({apiKey: process.env.CURSOR_API_KEY});
        loggedIn = true; email = user.email;
      } catch { /* A rejected explicit key must never use a saved login. */ }
    } else {
      const status = await Cursor.auth.status();
      loggedIn = status.status === 'logged-in'; email = status.email;
    }
    process.stdout.write(JSON.stringify({isAuthenticated: loggedIn, userInfo: {email}}) + '\n');
  } else {
    throw new Error('unsupported action');
  }
})().catch(() => {
  // SDK errors can include upstream responses. Keep account-action output credential-free.
  process.stderr.write('Cursor account action failed. Try signing in again.\n');
  process.exitCode = 1;
});
