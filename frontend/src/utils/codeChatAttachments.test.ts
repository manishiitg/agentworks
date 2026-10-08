import { expect, it } from 'vitest'
import { codeChatAttachmentFolder, codeChatAttachmentPaths } from './codeChatAttachments'
it('uses a stable safe session folder and excludes previous chats, folders and traversal', () => {
 const root = 'Chats/Code/projects/demo'
 const folder = codeChatAttachmentFolder(root, 'code:demo')
 expect(folder).toBe(`${root}/uploads/chats/Y29kZTpkZW1v`)
 expect(codeChatAttachmentPaths(root, 'code:demo', [
  {path:`${folder}/screen.png`,type:'file'}, {path:`${folder}/screen.png`,type:'file'},
  {path:`${folder}/../other.png`,type:'file'}, {path:`${folder}/nested/screen.png`,type:'file'},
  {path:`${root}/uploads/other.png`,type:'file'}, {path:`${folder}/folder`,type:'folder'},
 ])).toEqual([`${folder}/screen.png`])
})
