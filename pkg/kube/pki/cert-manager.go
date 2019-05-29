package pki

import (
	"github.com/jenkins-x/jx/pkg/kube"
	"github.com/jenkins-x/jx/pkg/log"
	"github.com/jenkins-x/jx/pkg/util"
	certmng "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1alpha1"
	certclient "github.com/jetstack/cert-manager/pkg/client/clientset/versioned"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// CertManagerNamespace indicates the namespace where is cert-manager deployed
	CertManagerNamespace = "cert-manager"
	// CertManagerDeployment indicates the name of the cert-manager deployment
	CertManagerDeployment = "cert-manager"
	// CertManagerReleaseName indicates the release name for cert-manager chart
	CertManagerReleaseName = "cert-manager"
	// CertManagerChart name of the cert-manager chart
	CertManagerChart = "stable/cert-manager"
	// CertManagerCRDsFile files which contains the cert-manager CRDs
	CertManagerCRDsFile = "http://raw.githubusercontent.com/jetstack/cert-manager/release-0.6/deploy/manifests/00-crds.yaml"

	// CertManagerIssuerProd name of the production issuer
	CertManagerIssuerProd       = "letsencrypt-prod"
	certManagerIssuerProdServer = "http://acme-v02.api.letsencrypt.org/directory"

	// CertManagerIssuerStaging name of the staging issuer
	CertManagerIssuerStaging       = "letsencrypt-staging"
	certManagerIssuerStagingServer = "http://acme-staging-v02.api.letsencrypt.org/directory"
)

// CleanCertManagerResources removed the cert-manager resources from the given namespaces
func CleanCertManagerResources(certclient certclient.Interface, ns string, ic kube.IngressConfig) error {
	if ic.Issuer == CertManagerIssuerProd {
		_, err := certclient.Certmanager().Issuers(ns).Get(CertManagerIssuerProd, metav1.GetOptions{})
		if err == nil {
			err := certclient.Certmanager().Issuers(ns).Delete(CertManagerIssuerProd, &metav1.DeleteOptions{})
			if err != nil {
				return errors.Wrapf(err, "deleting cert-manager issuer %q", CertManagerIssuerProd)
			}
		}
		_ = certclient.Certmanager().Certificates(ns).Delete(CertManagerIssuerProd, &metav1.DeleteOptions{})
	} else {
		_, err := certclient.Certmanager().Issuers(ns).Get(CertManagerIssuerStaging, metav1.GetOptions{})
		if err == nil {
			err := certclient.Certmanager().Issuers(ns).Delete(CertManagerIssuerStaging, &metav1.DeleteOptions{})
			if err != nil {
				return errors.Wrapf(err, "deleting cert-manager issuer %q", CertManagerIssuerStaging)
			}
		}
		_ = certclient.Certmanager().Certificates(ns).Delete(CertManagerIssuerStaging, &metav1.DeleteOptions{})
	}
	return nil
}

// CreateIssuer creates a cert-manager issuer according with the ingress configuration
func CreateIssuer(certclient certclient.Interface, ns string, ic kube.IngressConfig) error {
	if ic.Issuer == CertManagerIssuerProd {
		_, err := certclient.Certmanager().Issuers(ns).Get(CertManagerIssuerProd, metav1.GetOptions{})
		if err != nil {
			_, err := certclient.Certmanager().Issuers(ns).Create(
				issuer(CertManagerIssuerProd, certManagerIssuerProdServer, ic.Email))
			if err != nil {
				return errors.Wrapf(err, "creating cert-manager issuer %q", CertManagerIssuerProd)
			}
		}
	} else {
		_, err := certclient.Certmanager().Issuers(ns).Get(CertManagerIssuerStaging, metav1.GetOptions{})
		if err != nil {
			_, err := certclient.Certmanager().Issuers(ns).Create(
				issuer(CertManagerIssuerStaging, certManagerIssuerStagingServer, ic.Email))
			if err != nil {
				return errors.Wrapf(err, "creating cert-manager issuer %q", CertManagerIssuerStaging)
			}
		}
	}

	return nil
}

func issuer(name string, server string, email string) *certmng.Issuer {
	return &certmng.Issuer{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: certmng.IssuerSpec{
			IssuerConfig: certmng.IssuerConfig{
				ACME: &certmng.ACMEIssuer{
					Email:         email,
					Server:        server,
					SkipTLSVerify: false,
					PrivateKey: certmng.SecretKeySelector{
						LocalObjectReference: certmng.LocalObjectReference{
							Name: name,
						},
					},
					HTTP01: &certmng.ACMEIssuerHTTP01Config{},
				},
			},
		},
	}
}

// CreateCertManagerResources creates the cert-manager resources such as issuer in the target namespace
func CreateCertManagerResources(certclient certclient.Interface, targetNamespace string, ic kube.IngressConfig) error {
	if !ic.TLS {
		return nil
	}

	// do not recreate the issuer if it is already there and correctly configured
	if alreadyConfigured(certclient, targetNamespace, ic) {
		return nil
	}

	err := CleanCertManagerResources(certclient, targetNamespace, ic)
	if err != nil {
		return errors.Wrapf(err, "cleaning the cert-manager resources from namespace %q", targetNamespace)
	}

	err = CreateIssuer(certclient, targetNamespace, ic)
	if err != nil {
		return errors.Wrapf(err, "creating the cert-manager issuer %s/%s", targetNamespace, ic.Issuer)
	}

	return nil
}

// alreadyConfigured checks if cert-manager resources are already configured and match with the ingress configuration
func alreadyConfigured(certClient certclient.Interface, targetNamespace string, ingressConfig kube.IngressConfig) bool {
	issuer, err := certClient.CertmanagerV1alpha1().Issuers(targetNamespace).Get(ingressConfig.Issuer, metav1.GetOptions{})
	if err != nil {
		log.Infof("Certificate issuer %s does not exist. Creating...\n", util.ColorInfo(ingressConfig.Issuer))
		return false
	}
	// ingress and issuer email must match
	if issuer.Spec.ACME.Email != ingressConfig.Email {
		issuer.Spec.ACME.Email = ingressConfig.Email
		_, err := certClient.CertmanagerV1alpha1().Issuers(targetNamespace).Update(issuer)
		if err != nil {
			// can not update the issuer, let's assume it needs recreation
			log.Infof("Certificate issuer %s can not be updated. Recreating...\n", util.ColorInfo(ingressConfig.Issuer))
			return false
		}
	}
	log.Infof("Certificate issuer %s already configured.\n", util.ColorInfo(ingressConfig.Issuer))
	return true
}
