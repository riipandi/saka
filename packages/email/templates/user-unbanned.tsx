import { Hr } from 'react-email'
import { CardFooter, CardHeader, Text } from '../components'
import { sharedPreviewProps, sharedTemplateProps } from '../constants'
import { BaseTemplate } from '../layouts'

interface UserUnbannedData {
  name: string
}

interface UserUnbannedEmailProps {
  logoURL: string
  appName: string
  data: UserUnbannedData
}

export const UserUnbannedEmail = ({ logoURL, appName, data }: UserUnbannedEmailProps) => {
  return (
    <BaseTemplate logoURL={logoURL} appName={appName}>
      <CardHeader title='Account Reinstated' />
      <Hr style={{ marginTop: '16px' }} />
      <Text style={{ marginTop: '18px' }}>Hello {data.name},</Text>

      <Text>
        Your account has been reinstated by an administrator. You can sign in again with your usual
        credentials.
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

UserUnbannedEmail.TemplateProps = {
  ...sharedTemplateProps,
  data: {
    name: '{{.Data.Name}}'
  }
}

UserUnbannedEmail.PreviewProps = {
  ...sharedPreviewProps,
  data: {
    name: 'Hermione Granger'
  }
}

export default UserUnbannedEmail
