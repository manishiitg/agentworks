export interface CommandFrontmatter {
  name: string
  description: string
  icon?: string
  modes?: string[]
  show_on_card?: boolean
}

export interface UserCommand {
  frontmatter: CommandFrontmatter
  content: string
  folder_name: string
  file_path: string
}

export interface CreateCommandRequest {
  name: string
  content: string
}

export interface UpdateCommandRequest {
  content: string
}

export interface ListCommandsResponse {
  commands: UserCommand[]
  total: number
}
