import { Hr } from 'react-email'
import { Code, Alert, CardFooter, CardHeader, Text } from '../components'
import { sharedPreviewProps, sharedTemplateProps } from '../constants'
import { BaseTemplate } from '../layouts'

interface PasswordResetData {
  email: string
  resetCode: string
}

interface PasswordResetProps {
  logoURL: string
  appName: string
  data: PasswordResetData
}

export const PasswordReset = ({ logoURL, appName, data }: PasswordResetProps) => {
  return (
    <BaseTemplate logoURL={logoURL} appName={appName}>
      <CardHeader title='Reset Your Password' />
      <Hr style={{ marginTop: '16px' }} />
      <Text>Hello,</Text>

      <Text>
        You requested to reset your password for <strong>{data.email}</strong>.
      </Text>

      <Text>
        Use the code below to reset your password. Type it on the reset screen — the code works
        once.
      </Text>

      <Text style={{ textAlign: 'center' }}>
        <Code style={{ fontSize: '24px', letterSpacing: '4px' }}>{data.resetCode}</Code>
      </Text>

      <Alert variant='warning' style={{ marginTop: '20px' }}>
        <strong>Important:</strong> This code will expire in 1 hour. If you did not request this
        change, please ignore this email.
      </Alert>

      <CardFooter>
        <Text size='sm' style={{ marginBottom: '4px' }}>
          You&apos;re receiving this email because you have an account in {appName}. <br />
          If you are not sure why you&apos;re receiving this, please contact us.
        </Text>
      </CardFooter>
    </BaseTemplate>
  )
}

PasswordReset.TemplateProps = {
  ...sharedTemplateProps,
  data: {
    email: '{{.Data.Email}}',
    resetCode: '{{.Data.ResetCode}}'
  }
}

PasswordReset.PreviewProps = {
  ...sharedPreviewProps,
  data: {
    email: 'user@example.com',
    resetCode: 'EXPECTO-PATRONUM'
  }
}

export default PasswordReset
