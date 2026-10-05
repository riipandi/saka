import { Hr } from 'react-email'
import { Alert, CardFooter, CardHeader, Text } from '../components'
import { sharedPreviewProps, sharedTemplateProps } from '../constants'
import { BaseTemplate } from '../layouts'

interface PasswordChangedNoticeData {
  name: string
  email: string
}

interface PasswordChangedNoticeProps {
  logoURL: string
  appName: string
  data: PasswordChangedNoticeData
}

export const PasswordChangedNotice = ({ logoURL, appName, data }: PasswordChangedNoticeProps) => {
  return (
    <BaseTemplate logoURL={logoURL} appName={appName}>
      <CardHeader title='Your Password Was Changed' />
      <Hr style={{ marginTop: '16px' }} />
      <Text>Hello {data.name},</Text>

      <Text>
        The password for <strong>{data.email}</strong> was just changed through a password reset.
      </Text>

      <Alert variant='info' style={{ marginTop: '20px' }}>
        <strong>Security Notice:</strong> All active sessions have been signed out for security. If
        this was you, you can sign in with your new password.
      </Alert>

      <Text>
        If you did <strong>not</strong> make this change, please contact support immediately — your
        account may be at risk.
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

PasswordChangedNotice.TemplateProps = {
  ...sharedTemplateProps,
  data: {
    name: '{{.Data.Name}}',
    email: '{{.Data.Email}}'
  }
}

PasswordChangedNotice.PreviewProps = {
  ...sharedPreviewProps,
  data: {
    name: 'Robert Langdon',
    email: 'user@example.com'
  }
}

export default PasswordChangedNotice
