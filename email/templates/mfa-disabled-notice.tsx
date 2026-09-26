import { Hr } from 'react-email'
import { CardFooter, CardHeader, Text } from '../components'
import { sharedPreviewProps, sharedTemplateProps } from '../constants'
import { BaseTemplate } from '../layouts'

interface MfaDisabledNoticeData {
  name: string
  reason: string
}

interface MfaDisabledNoticeEmailProps {
  logoURL: string
  appName: string
  data: MfaDisabledNoticeData
}

export const MfaDisabledNoticeEmail = ({ logoURL, appName, data }: MfaDisabledNoticeEmailProps) => {
  return (
    <BaseTemplate logoURL={logoURL} appName={appName}>
      <CardHeader title='Two-Factor Authentication Removed' warning />
      <Hr style={{ marginTop: '16px' }} />
      <Text style={{ marginTop: '18px' }}>Hello {data.name},</Text>

      <Text>
        Two-factor authentication has been removed from your account by an administrator. You can
        now sign in with your password alone.
      </Text>

      {data.reason ? (
        <Text>
          Reason: <strong>{data.reason}</strong>
        </Text>
      ) : (
        <Text>If you are not sure why this was done, please contact us.</Text>
      )}

      <Text>
        If you did not expect this, your account may be at risk — please change your password and
        contact us immediately.
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

MfaDisabledNoticeEmail.TemplateProps = {
  ...sharedTemplateProps,
  data: {
    name: '{{.Data.Name}}',
    // Empty when the operator wrote none: the template renders its own case.
    reason: '{{.Data.Reason}}'
  }
}

MfaDisabledNoticeEmail.PreviewProps = {
  ...sharedPreviewProps,
  data: {
    name: 'Hermione Granger',
    reason: 'Lost authenticator device, verified over support'
  }
}

export default MfaDisabledNoticeEmail
