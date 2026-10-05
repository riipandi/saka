import { Hr } from 'react-email'
import { CardFooter, CardHeader, Text } from '../components'
import { sharedPreviewProps, sharedTemplateProps } from '../constants'
import { BaseTemplate } from '../layouts'

interface UserBannedData {
  name: string
  reason: string
  expiresAt: string
}

interface UserBannedEmailProps {
  logoURL: string
  appName: string
  data: UserBannedData
}

export const UserBannedEmail = ({ logoURL, appName, data }: UserBannedEmailProps) => {
  return (
    <BaseTemplate logoURL={logoURL} appName={appName}>
      <CardHeader title='Account Suspended' warning />
      <Hr style={{ marginTop: '16px' }} />
      <Text style={{ marginTop: '18px' }}>Hello {data.name},</Text>

      <Text>
        Your account has been suspended by an administrator and can no longer be used to sign in.
      </Text>

      <Text>
        Reason: <strong>{data.reason}</strong>
      </Text>

      {data.expiresAt ? (
        <Text>
          The suspension lifts on <strong>{data.expiresAt}</strong>, after which you can sign in
          again.
        </Text>
      ) : (
        <Text>
          This suspension does not lift on its own. If you believe it was applied in error, please
          contact us.
        </Text>
      )}

      <CardFooter>
        <Text size='sm' style={{ marginBottom: '4px' }}>
          You&apos;re receiving this email because you have an account in {appName}. <br />
          If you are not sure why you&apos;re receiving this, please contact us.
        </Text>
      </CardFooter>
    </BaseTemplate>
  )
}

UserBannedEmail.TemplateProps = {
  ...sharedTemplateProps,
  data: {
    name: '{{.Data.Name}}',
    reason: '{{.Data.Reason}}',
    // Empty when the ban never lifts: the template renders its absence.
    expiresAt: '{{.Data.ExpiresAt}}'
  }
}

UserBannedEmail.PreviewProps = {
  ...sharedPreviewProps,
  data: {
    name: 'Hermione Granger',
    reason: 'Repeated violations of the community guidelines',
    expiresAt: 'October 7, 2024'
  }
}

export default UserBannedEmail
