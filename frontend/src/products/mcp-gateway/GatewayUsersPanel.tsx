import UsersAdminPanel from '../../components/admin/UsersAdminPanel'

/** Accounts, roles and product access use the full product's user directory. */
export function GatewayUsersPanel({ base: _base }: { base: string }) {
  return <div data-testid="gateway-users"><UsersAdminPanel /></div>
}
