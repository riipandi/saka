import { Hr } from 'react-email'
import { Code, CardFooter, CardHeader, Text } from '../components'
import { sharedPreviewProps, sharedTemplateProps } from '../constants'
import { BaseTemplate } from '../layouts'

interface EmailVerificationData {
  userFullName: string
  verificationCode: string
}

interface EmailVerificationProps {
  logoURL: string
  appName: string
  data: EmailVerificationData
}

export const EmailVerification = ({ logoURL, appName, data }: EmailVerificationProps) => {
  return (
    <BaseTemplate logoURL={logoURL} appName={appName}>
      <CardHeader title='Email Verification' />
      <Hr style={{ marginTop: '16px' }} />
      <Text style={{ marginTop: '18px' }}>Hello {data.userFullName},</Text>

      <Text>
        Use the code below to verify your email address for {appName}. Type it on the verification
        screen — the code works once.
      </Text>

      <Text style={{ textAlign: 'center' }}>
        <Code style={{ fontSize: '24px', letterSpacing: '4px' }}>{data.verificationCode}</Code>
      </Text>

      <Text style={{ marginTop: '24px' }}>
        <strong>Important:</strong> This code will expire in 1 hour.
      </Text>

      <Text>If you did not create account, please ignore this email.</Text>

      <CardFooter>
        <Text size='sm' style={{ marginBottom: '4px' }}>
          You&apos;re receiving this email because you have an account in {appName}. <br />
          If you are not sure why you&apos;re receiving this, please contact us.
        </Text>
      </CardFooter>
    </BaseTemplate>
  )
}

EmailVerification.TemplateProps = {
  ...sharedTemplateProps,
  data: {
    userFullName: '{{.Data.UserFullName}}',
    verificationCode: '{{.Data.VerificationCode}}'
  }
}

EmailVerification.PreviewProps = {
  ...sharedPreviewProps,
  data: {
    userFullName: 'John Doe',
    verificationCode: 'GRYFFINDOR-EXPECTO'
  }
}

export default EmailVerification
