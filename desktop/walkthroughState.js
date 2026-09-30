const fs = require('fs');
const path = require('path');

const validKey = key => typeof key === 'string' &&
  (/^agentworks_tip_(agentworks|crew|code|providers)_[a-z0-9_]{1,80}_v\d+_dismissed$/.test(key) ||
  /^agentworks_(overview|empty_automation|automation|empty_crew|crew|empty_code|code|providers)_walkthrough_v\d+_dismissed$/.test(key));

// Renderer localStorage belongs to a localhost port. Keep desktop tour choices
// in the app profile so changing the server/dev port does not reset them.
function createWalkthroughState(userDataPath) {
  const file = path.join(userDataPath, 'walkthrough-dismissals.json');
  let dismissed = new Set();
  try {
    const saved = JSON.parse(fs.readFileSync(file, 'utf8'));
    if (Array.isArray(saved)) dismissed = new Set(saved.filter(validKey));
  } catch (_) {
    // First launch, or an unreadable file: the app still opens normally.
  }
  return {
    getDismissed: () => [...dismissed],
    dismiss(key) {
      if (!validKey(key) || dismissed.has(key)) return;
      dismissed.add(key);
      fs.mkdirSync(userDataPath, { recursive: true });
      const temporary = `${file}.tmp`;
      fs.writeFileSync(temporary, JSON.stringify([...dismissed]));
      fs.renameSync(temporary, file);
    },
  };
}

module.exports = { createWalkthroughState };
