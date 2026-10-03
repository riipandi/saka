import { Hr, CodeInline } from 'react-email'
import { CardFooter, CardHeader, Text } from '../components'
import { sharedPreviewProps, sharedTemplateProps } from '../constants'
import { BaseTemplate } from '../layouts'

interface ReauthenticationCodeData {
  code: string
  expirationString: string
}

interface ReauthenticationCodeEmailProps {
  logoURL: string
  appName: string
  data: ReauthenticationCodeData
}

export const ReauthenticationCodeEmail = ({
  logoURL,
  appName,
  data
}: ReauthenticationCodeEmailProps) => {
  return (
    <BaseTemplate logoURL={logoURL} appName={appName}>
      <CardHeader title='Confirm It Is You' />
      <Hr style={{ marginTop: '16px' }} />

      <Text style={{ marginTop: '18px' }}>
        Enter this one-time code on the confirmation screen to continue. The code works once.
      </Text>

      <Text style={{ textAlign: 'center' }}>
        <CodeInline style={{ fontSize: '24px', letterSpacing: '4px' }}>{data.code}</CodeInline>
      </Text>

      <Hr style={{ marginTop: '24px' }} />

      <Text style={{ marginTop: '24px' }}>
        <strong>Important:</strong> This code will expire in {data.expirationString}.
      </Text>

      <Text>
        If you did not make this request, please protect your account and sign out of your other
        sessions.
      </Text>

      <CardFooter>
        <Text size='sm' style={{ marginBottom: '4px' }}>
          You&apos;re receiving this email because a sensitive action on your {appName} account
          asked for a confirmation. <br />
          If you are not sure why you&apos;re receiving this, please contact us.
        </Text>
      </CardFooter>
    </BaseTemplate>
  )
}

ReauthenticationCodeEmail.TemplateProps = {
  ...sharedTemplateProps,
  data: {
    code: '{{.Data.Code}}',
    expirationString: '{{.Data.ExpirationString}}'
  }
}

ReauthenticationCodeEmail.PreviewProps = {
  ...sharedPreviewProps,
  data: {
    code: 'EXPECTOPATRONUM',
    expirationString: '15 minutes'
  }
}

export default ReauthenticationCodeEmail
