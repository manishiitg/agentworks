import { HowToAnswers } from '../HowToAnswers'

type GmailHowToGuideProps = {
  scopeNoun: 'workflow' | 'project'
}

export function GmailHowToGuide({ scopeNoun }: GmailHowToGuideProps) {
  const questions = [
    {
      title: 'How do I connect my first account?',
      answer: <>In <b>Connect a Google account</b>, use the <b>Company Google app</b> when an administrator has configured it. Locally, select a saved app or <b>Use my own OAuth JSON</b>, name the app, and upload its Google Cloud client JSON. Choose access and click <b>Connect Google account</b>, then finish Google consent. The First-time setup guide is available with JSON upload.</>,
    },
    {
      title: 'Which access should I choose?',
      answer: <>Notifications need only Gmail send access. <b>Reading this mailbox</b> allows search and reading; <b>agent drafts and send/reply</b> is a separate permission. Drive, Sheets, Docs, Slides, and Calendar are optional and start as read-only. Turn on write access for a service only when this {scopeNoun} must create or edit there.</>,
    },
    {
      title: 'How do I change access later?',
      answer: <>On the account row, click <b>Change access</b>, add or remove services, choose their access levels, then click <b>Sign in again with Google</b>. <b>AgentWorks settings</b> shows the saved choices; <b>Google</b> shows the granted permissions separately. The connection keeps its existing OAuth app.</>,
    },
    {
      title: 'How do I add another mailbox?',
      answer: <>Use <b>Connect a Google account</b> again. You can reuse the company app or a saved named app without another upload. If that app is in Testing, add the new address as a test user first. In the account’s More menu, choose <b>Make default</b> for the default notification sender.</>,
    },
    {
      title: 'Which account sends, and who receives?',
      answer: <>Click <b>Make default</b> on a connected sending account. It is used unless work explicitly selects another connected account. In <b>Delivery settings</b>, enter comma-separated <b>Default recipients</b>, add any <b>Disallowed recipients</b>, and click <b>Save</b>.</>,
    },
    {
      title: 'How do I test delivery?',
      answer: <>Use <b>Send a test email</b> in an account’s More menu to test that sender. To test the account-wide delivery settings, enter a default recipient and click <b>Send test email</b>. If the test is unavailable, check that an account is connected and the recipient is not disallowed.</>,
    },
    {
      title: 'How do I pause email?',
      answer: <>Turn off <b>Enable Gmail</b> under <b>Delivery settings</b> and click <b>Save</b> to stop all outgoing notifications. To stop only one sender, use <b>Turn off</b> in its More menu. You can use <b>Turn on</b> there when you need it again.</>,
    },
    {
      title: 'Why is Google sign-in blocked?',
      answer: <>If the Google app is still in <b>Testing</b>, add this mailbox under <b>Google Auth Platform → Audience → Test users</b>. For a hosted AgentWorks server, the OAuth client must be a <b>Web application</b> with the exact redirect URI shown in the First-time setup guide. Use <b>Copy link</b> if you need to sign in from a different Chrome profile.</>,
    },
    {
      title: 'Why did a new permission not take effect?',
      answer: <>Compare <b>AgentWorks settings</b> with the <b>Google</b> permissions. If Google grants Gmail reading but AgentWorks has it disabled, use <b>Change access</b> to enable it. If selected permissions are missing from Google, finish a successful reconnect. A failed callback keeps the previous grant; check the sign-in error before changing the Cloud app.</>,
    },
    {
      title: 'Can people reply to notification emails?',
      answer: <>These emails are one-way notifications. A reply does not resume a {scopeNoun === 'project' ? 'Crew conversation' : 'workflow run'}. Mailbox reading and agent-authored replies require separate access and are configured above.</>,
    },
  ]

  return <HowToAnswers
    topic="Gmail"
    description="Open a question for the steps you need. For full Google Cloud setup, expand First-time setup guide in the Gmail panel."
    questions={questions}
  />
}
