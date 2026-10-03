import { Hr } from 'react-email'
import { Alert, CardFooter, CardHeader, Text } from '../components'
import { sharedPreviewProps, sharedTemplateProps } from '../constants'
import { BaseTemplate } from '../layouts'

interface UserLockedData {
  name: string
  email: string
  expiresAt: string
}

interface UserLockedProps {
  logoURL: string
  appName: string
  data: UserLockedData
}

export const UserLocked = ({ logoURL, appName, data }: UserLockedProps) => {
  const window = data.expiresAt
    ? `until ${data.expiresAt}`
    : 'until an administrator unlocks the account'

  return (
    <BaseTemplate logoURL={logoURL} appName={appName}>
      <CardHeader title='Your Account Was Locked' />
      <Hr style={{ marginTop: '16px' }} />
      <Text>Hello {data.name},</Text>

      <Text>
        The account <strong>{data.email}</strong> was locked {window} after too many failed sign-in
        attempts with the password.
      </Text>

      <Alert variant='warning' style={{ marginTop: '20px' }}>
        <strong>Security Notice:</strong> If you tried to sign in just now, wait for the window to
        pass and try again — or reset your password to regain access sooner.
      </Alert>

      <Text>
        If you did <strong>not</strong> try to sign in, someone may have your password. Please reset
        it now.
      </Text>

      <CardFooter>
        <Text size='sm' style={{ marginBottom: '4px' }}>
          You&apos;re receiving this email because you have an account in {appName}. <br />
          If you are not sure why you&apos;re receiving this, please contact us.
        </Text>
      </CardFooter>
    </BaseTemplate>
  )
}

UserLocked.TemplateProps = {
  ...sharedTemplateProps,
  data: {
    name: '{{.Data.Name}}',
    email: '{{.Data.Email}}',
    expiresAt: '{{.Data.ExpiresAt}}'
  }
}

UserLocked.PreviewProps = {
  ...sharedPreviewProps,
  data: {
    name: 'Robert Langdon',
    reason: 'langdon@example.com',
    expiresAt: ''
  }
}

export default UserLocked
